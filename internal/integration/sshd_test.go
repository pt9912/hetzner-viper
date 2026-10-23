package integration

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/machine"
	"github.com/pt9912/hetzner-viper/internal/servertest"
)

// Trust logic against a real OpenSSH pair: the ssh config is written by the same code the commands
// use, sshd runs unprivileged on 127.0.0.1. Needs sshd (and /run/sshd); skipped elsewhere.

func haveRealSshd(t *testing.T) {
	t.Helper()
	for _, p := range []string{"/usr/sbin/sshd", "/usr/bin/ssh"} {
		if _, err := os.Stat(p); err != nil {
			servertest.SkipOrFail(t, "no real sshd: %s", p)
		}
	}
	if st, err := os.Stat("/run/sshd"); err != nil || !st.IsDir() {
		servertest.SkipOrFail(t, "no /run/sshd")
	}
}

type realSshd struct {
	t    *testing.T
	e    *Env
	dir  string
	port int
	cmd  *exec.Cmd
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// start runs sshd with a host key signed by the CA for claude-web.example.com, valid for validity.
func (s *realSshd) start(validity string) {
	t := s.t
	t.Helper()
	s.stop()
	d := s.dir
	os.MkdirAll(d, 0o755)
	for _, f := range []string{"hk", "hk.pub", "hk-cert.pub"} {
		os.Remove(filepath.Join(d, f))
	}
	run := func(args ...string) {
		if out, err := exec.Command("ssh-keygen", args...).CombinedOutput(); err != nil {
			t.Fatalf("ssh-keygen %v: %v\n%s", args, err, out)
		}
	}
	run("-q", "-t", "ed25519", "-N", "", "-C", "claude-web", "-f", filepath.Join(d, "hk"))
	run("-q", "-s", filepath.Join(s.e.Home, ".ssh", "hetzner_claude_host_ca"), "-I", "t", "-h", "-n", "claude-web.example.com", "-V", validity, filepath.Join(d, "hk.pub"))
	pub, _ := os.ReadFile(filepath.Join(s.e.Home, ".ssh", "hetzner-build.pub"))
	os.WriteFile(filepath.Join(d, "authorized_keys"), pub, 0o600)
	cfg := fmt.Sprintf("Port %d\nListenAddress 127.0.0.1\nHostKey %s\nHostCertificate %s\nPidFile %s\nUsePAM no\nStrictModes no\nAuthorizedKeysFile %s\n",
		s.port, filepath.Join(d, "hk"), filepath.Join(d, "hk-cert.pub"), filepath.Join(d, "pid"), filepath.Join(d, "authorized_keys"))
	os.WriteFile(filepath.Join(d, "sshd_config"), []byte(cfg), 0o644)
	s.cmd = exec.Command("/usr/sbin/sshd", "-D", "-e", "-f", filepath.Join(d, "sshd_config"))
	if err := s.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", s.port), 100*time.Millisecond); err == nil {
			c.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("sshd did not start")
}

func (s *realSshd) stop() {
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
		s.cmd.Wait()
		s.cmd = nil
	}
}

func (s *realSshd) pin(pubFile string) {
	b, _ := os.ReadFile(pubFile)
	f := strings.Fields(string(b))
	os.WriteFile(filepath.Join(s.e.Home, ".ssh", "known_hosts_hetzner_claude-web"), []byte("claude-web.example.com "+f[0]+" "+f[1]+"\n"), 0o600)
}

// config writes the ssh config like start and stop do.
func (s *realSshd) config() {
	if err := machine.WriteSSHConfig(s.e.Config("web"), "127.0.0.1"); err != nil {
		s.t.Fatal(err)
	}
}

// ssh connects via the commands' own config (user and port overridden on the command line); it
// returns the error output.
func (s *realSshd) ssh() (string, error) {
	u, _ := user.Current()
	cmd := exec.Command("/usr/bin/ssh", "-n", "-F", filepath.Join(s.e.Home, ".ssh", "config_hetzner_claude-web"), "-l", u.Username, "-p", fmt.Sprint(s.port), "-o", "BatchMode=yes", "hetzner-claude-web", "true")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func newRealSshd(t *testing.T) *realSshd {
	t.Helper()
	haveRealSshd(t)
	e := New(t, "")
	s := &realSshd{t: t, e: e, dir: filepath.Join(t.TempDir(), "sshd"), port: freePort(t)}
	t.Cleanup(s.stop)
	return s
}

func TestRealSshTrustLogic(t *testing.T) {
	s := newRealSshd(t)
	hk := filepath.Join(s.dir, "hk.pub")

	s.start("-5m:+7d")
	s.pin(hk)
	s.config()
	if out, err := s.ssh(); err != nil {
		t.Errorf("pinned key + valid cert: %v\n%s", err, out)
	}

	s.start("20200101:20200102")
	s.pin(hk)
	s.config()
	if out, err := s.ssh(); err != nil {
		t.Errorf("pinned key + expired cert: %v\n%s", err, out)
	}

	other := filepath.Join(t.TempDir(), "old")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", other).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	s.start("-5m:+7d")
	s.pin(other + ".pub")
	s.config()
	out, err := s.ssh()
	if err == nil {
		t.Error("a stale pin must be rejected despite a valid CA cert")
	}
	if k := machine.SSHFailureKind(out); k != "hostkey" {
		t.Errorf("stale pin: kind %s\n%s", k, out)
	}

	s.start("-5m:+7d")
	os.Remove(filepath.Join(s.e.Home, ".ssh", "known_hosts_hetzner_claude-web"))
	s.config()
	if out, err := s.ssh(); err != nil {
		t.Errorf("no pin: CA fallback with a valid cert: %v\n%s", err, out)
	}

	s.start("20200101:20200102")
	s.config()
	out, err = s.ssh()
	if err == nil || machine.SSHFailureKind(out) != "hostkey" {
		t.Errorf("no pin + expired cert is a host key problem: %v kind %s\n%s", err, machine.SSHFailureKind(out), out)
	}

	s.stop()
	out, err = s.ssh()
	if err == nil || machine.SSHFailureKind(out) != "network" {
		t.Errorf("nothing listening is a network problem: %v kind %s\n%s", err, machine.SSHFailureKind(out), out)
	}
}

func TestRealSshIgnoresTheUsersForwardAgent(t *testing.T) {
	s := newRealSshd(t)
	s.config()
	home := s.e.Home
	// the user's own config enables agent forwarding for every host, below our Include line
	os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte("Include "+filepath.Join(home, ".ssh", "config_hetzner_claude-web")+"\nHost *\n    ForwardAgent yes\n    ForwardX11 yes\n"), 0o600)
	cmd := exec.Command("/usr/bin/ssh", "-G", "-F", filepath.Join(home, ".ssh", "config"), "hetzner-claude-web")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"\nforwardagent no\n", "\nforwardx11 no\n"} {
		if !strings.Contains("\n"+string(out), want) {
			t.Errorf("missing %q in\n%s", strings.TrimSpace(want), out)
		}
	}
}
