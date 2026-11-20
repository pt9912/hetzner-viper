package cli_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/system"
	"github.com/pt9912/hetzner-viper/internal/adapter/driving/cli"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

// A tunnel that dies and is started again on the same local port: does a NATS client that is connected through it carry on? This is
// the question of docs/plan/nats-lokal.md §4.3 (a laptop sleeps and ssh dies). It needs a real sshd, which only a Linux machine with
// OpenSSH has; elsewhere the test is skipped. Only own processes are started and ended (by their handles).

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func waitPort(t *testing.T, port int) {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond); err == nil {
			_ = c.Close()
			return
		}
	}
	t.Fatalf("port %d never listens", port)
}

// sshdWorld is a real sshd of this user on a port of its own, with the NATS server behind it.
type sshdWorld struct {
	dir, ssh      string
	sshPort, nats int
}

// startSSHD starts sshd (skips the test where there is none) and a NATS server that the tunnels lead to.
func startSSHD(t *testing.T) *sshdWorld {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the real sshd of this measurement is for Linux")
	}
	sshd, err := exec.LookPath("sshd")
	if err != nil {
		t.Skip("no sshd")
	}
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("no ssh")
	}
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("no ssh-keygen")
	}
	dir := t.TempDir()
	// the NATS server
	ns, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("server not ready")
	}
	t.Cleanup(ns.Shutdown)
	natsPort := ns.Addr().(*net.TCPAddr).Port
	// keys and sshd as this user, on a port of its own
	for _, k := range []string{"host", "user"} {
		if out, err := exec.Command(keygen, "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(dir, k)).CombinedOutput(); err != nil {
			t.Fatalf("ssh-keygen: %v %s", err, out)
		}
	}
	pub, _ := os.ReadFile(filepath.Join(dir, "user.pub"))
	if err := os.WriteFile(filepath.Join(dir, "authorized_keys"), pub, 0o600); err != nil {
		t.Fatal(err)
	}
	sshPort := freePort(t)
	conf := fmt.Sprintf("Port %d\nListenAddress 127.0.0.1\nHostKey %s\nAuthorizedKeysFile %s\nPidFile %s\nUsePAM no\nPasswordAuthentication no\nStrictModes no\nAllowTcpForwarding yes\nLogLevel ERROR\n",
		sshPort, filepath.Join(dir, "host"), filepath.Join(dir, "authorized_keys"), filepath.Join(dir, "sshd.pid"))
	if err := os.WriteFile(filepath.Join(dir, "sshd_config"), []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	sd := exec.Command(sshd, "-D", "-f", filepath.Join(dir, "sshd_config"), "-e")
	if err := sd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sd.Process.Kill(); _, _ = sd.Process.Wait() })
	waitPort(t, sshPort)

	return &sshdWorld{dir: dir, ssh: ssh, sshPort: sshPort, nats: natsPort}
}

func TestANatsClientCarriesOnWhenTheTunnelIsStartedAgainOnTheSamePort(t *testing.T) {
	w := startSSHD(t)
	dir, ssh, sshPort, natsPort := w.dir, w.ssh, w.sshPort, w.nats
	local := freePort(t)
	startTunnel := func() *exec.Cmd {
		cmd := exec.CommandContext(context.Background(), ssh, "-N", "-L", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", local, natsPort), "-p", fmt.Sprint(sshPort),
			"-i", filepath.Join(dir, "user"), "-F", "/dev/null", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ExitOnForwardFailure=yes",
			"-o", "IdentitiesOnly=yes", "-o", "ServerAliveInterval=2", "-o", "ServerAliveCountMax=2", "127.0.0.1")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		waitPort(t, local)
		return cmd
	}
	tun := startTunnel()
	t.Cleanup(func() { _ = tun.Process.Kill(); _, _ = tun.Process.Wait() })

	var reconnects atomic.Int32
	nc, err := nats.Connect(fmt.Sprintf("nats://127.0.0.1:%d", local), nats.MaxReconnects(-1), nats.ReconnectWait(200*time.Millisecond), nats.Timeout(2*time.Second),
		nats.ReconnectHandler(func(*nats.Conn) { reconnects.Add(1) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	got := make(chan string, 4)
	if _, err := nc.Subscribe("m0.ping", func(m *nats.Msg) { got <- string(m.Data) }); err != nil {
		t.Fatal(err)
	}
	roundtrip := func(text string) bool {
		if err := nc.Publish("m0.ping", []byte(text)); err != nil {
			return false
		}
		_ = nc.FlushTimeout(time.Second)
		select {
		case s := <-got:
			return s == text
		case <-time.After(time.Second):
			return false
		}
	}
	if !roundtrip("before") {
		t.Fatal("no round trip through the tunnel")
	}
	// the tunnel dies, as when a laptop sleeps
	_ = tun.Process.Kill()
	_, _ = tun.Process.Wait()
	died := time.Now()
	for nc.IsConnected() && time.Since(died) < 10*time.Second {
		time.Sleep(20 * time.Millisecond)
	}
	t.Logf("the client noticed the dead tunnel after %v", time.Since(died).Round(10*time.Millisecond))
	if nc.IsConnected() {
		t.Fatal("the client does not notice a dead tunnel")
	}
	// started again on the same local port
	began := time.Now()
	tun = startTunnel()
	t.Logf("the tunnel listens again after %v", time.Since(began).Round(10*time.Millisecond))
	for !nc.IsConnected() && time.Since(began) < 15*time.Second {
		time.Sleep(20 * time.Millisecond)
	}
	if !nc.IsConnected() {
		t.Fatal("the client did not reconnect through the new tunnel")
	}
	t.Logf("the client is connected again %v after the new tunnel (reconnects: %d)", time.Since(began).Round(10*time.Millisecond), reconnects.Load())
	if !roundtrip("after") {
		t.Fatal("no round trip after the tunnel was started again")
	}
	if !nc.IsConnected() {
		t.Fatal("not connected")
	}
}

func TestSSHTunnelTakesTheWantedPortAndNamesALeftoverOne(t *testing.T) {
	w := startSSHD(t)
	conf := filepath.Join(w.dir, "ssh_config")
	text := fmt.Sprintf("Host sshdtest\n  HostName 127.0.0.1\n  Port %d\n  IdentityFile %s\n  IdentitiesOnly yes\n  StrictHostKeyChecking no\n  UserKnownHostsFile /dev/null\n  ServerAliveInterval 2\n",
		w.sshPort, filepath.Join(w.dir, "user"))
	if err := os.WriteFile(conf, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	h := testHost()
	h.StreamTool = system.Stream
	via := driving.NATSTarget{Config: conf, Alias: "sshdtest"}
	want := freePort(t)
	addr, stop, err := cli.SSHTunnel(context.Background(), h, via, "127.0.0.1", w.nats, want)
	if err != nil {
		t.Fatal(err)
	}
	if addr != fmt.Sprintf("127.0.0.1:%d", want) {
		t.Errorf("the tunnel is on %s, not on %d", addr, want)
	}
	nc, err := nats.Connect("nats://"+addr, nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatalf("through the tunnel: %v", err)
	}
	nc.Close()
	// the port is taken now: a second tunnel says so, and names a leftover ssh as the likely reason
	if _, _, err := cli.SSHTunnel(context.Background(), h, via, "127.0.0.1", w.nats, want); err == nil || !strings.Contains(err.Error(), "is taken") {
		t.Errorf("a taken port: %v", err)
	}
	stop() // ssh has ended when stop returns: the port is free again
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("the port stays taken after stop: %v", err)
	}
	_ = l.Close()
}
