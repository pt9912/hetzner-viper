package cli_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/settingsfile"
	"github.com/pt9912/hetzner-viper/internal/adapter/driving/cli"
	"github.com/pt9912/hetzner-viper/internal/natsmcp"
	"github.com/pt9912/hetzner-viper/internal/natsserver"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

// fakeOp is the registration of the operator at the server.
type fakeOp struct {
	registered map[string]string // name -> public key
	revoked    []string
	findErr    error
	taken      map[string]bool // names of machines
}

func (f *fakeOp) Find(_ context.Context, ms []driving.NATSMachine) (driving.NATSServerInfo, error) {
	if f.findErr != nil {
		return driving.NATSServerInfo{}, f.findErr
	}
	return driving.NATSServerInfo{Machine: ms[0], Host: "10.0.0.2", Port: 4222}, nil
}

func (f *fakeOp) Register(_ context.Context, _ string, _ driving.NATSServerInfo, name, pub string) error {
	if f.taken[name] {
		return natsserver.ErrNameTaken
	}
	f.registered[name] = pub
	return nil
}

func (f *fakeOp) Revoke(_ context.Context, _ string, _ driving.NATSServerInfo, name string) error {
	f.revoked = append(f.revoked, name)
	delete(f.registered, name)
	return nil
}

type quietLog struct{}

func (quietLog) Printf(string, ...any) {}

// operatorKey is where viper nats init puts the key of the group dev.
func operatorKey(t *testing.T, dir string, env settings.Env) string {
	t.Helper()
	c, err := settingsfile.Load(dir, "alpha", env, false)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(c.StateDir, "nats", "dev", "operator.nk")
}

func TestInitMakesTheKeyOnceAndRegistersItAndRevokeRemovesBoth(t *testing.T) {
	dir, env := natsGroup(t, "")
	op := &fakeOp{registered: map[string]string{}}
	run := func(sub string, p cli.Opts) (string, error) {
		_, h := fakeTools(t)
		h.Env = func() (settings.Env, error) { return env, nil }
		h.Services.NATSOp = op
		var out strings.Builder
		h.Stdout = &out
		p.Host, p.Dir, p.Args = h, dir, []string{sub, "dev"}
		err := cli.RunCommand("nats", &p)
		return out.String(), err
	}
	out, err := run("init", cli.Opts{})
	if err != nil || !strings.Contains(out, "operator is registered") {
		t.Fatalf("%v\n%s", err, out)
	}
	key := operatorKey(t, dir, env)
	st, err := os.Stat(key)
	if err != nil || (runtime.GOOS != "windows" && st.Mode().Perm() != 0o600) { // Windows has no permission bits
		t.Fatalf("the key: %v %v", st, err)
	}
	if dst, _ := os.Stat(filepath.Dir(key)); runtime.GOOS != "windows" && dst.Mode().Perm()&0o077 != 0 {
		t.Errorf("the directory is open to others: %v", dst.Mode())
	}
	seed, _ := os.ReadFile(key)
	pub, _ := natsserver.PublicKeyOf(string(seed))
	if op.registered["operator"] != pub {
		t.Errorf("registered %q, key %q", op.registered["operator"], pub)
	}
	// a second init keeps the key
	if _, err := run("init", cli.Opts{}); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(key); string(again) != string(seed) {
		t.Error("init made a new key")
	}
	if _, err := run("revoke", cli.Opts{}); err != nil || len(op.revoked) != 1 || op.revoked[0] != "operator" {
		t.Fatalf("%v %v", err, op.revoked)
	}
	if _, err := os.Stat(key); !os.IsNotExist(err) {
		t.Errorf("the key stays after revoke: %v", err)
	}
}

func TestInitWhenTheServerIsNotFoundOrTheNameIsTakenChangesNothing(t *testing.T) {
	dir, env := natsGroup(t, "")
	_, h := fakeTools(t)
	h.Env = func() (settings.Env, error) { return env, nil }
	op := &fakeOp{registered: map[string]string{}, findErr: errors.New("none of the machines is the NATS server of the group")}
	h.Services.NATSOp = op
	if err := cli.RunCommand("nats", &cli.Opts{Host: h, Dir: dir, Args: []string{"init", "dev"}}); err == nil {
		t.Fatal("no server")
	}
	if _, err := os.Stat(operatorKey(t, dir, env)); !os.IsNotExist(err) {
		t.Error("a key was made although nothing could be registered")
	}
	// the name belongs to a machine at the server
	op.findErr, op.taken = nil, map[string]bool{"operator": true}
	if err := cli.RunCommand("nats", &cli.Opts{Host: h, Dir: dir, Args: []string{"init", "dev"}}); err == nil || !errors.Is(err, natsserver.ErrNameTaken) {
		t.Errorf("%v", err)
	}
}

func TestAMachineWithTheNameOfTheOperatorIsRefused(t *testing.T) {
	dir := upGroupDir(t, "alpha,beta", "NATS_MCP=true\nNATS_OPERATOR_NAME=beta\n")
	_, err := settingsfile.Load(dir, "beta", upEnv(t), false)
	if err == nil || !strings.Contains(err.Error(), "name of the operator") {
		t.Errorf("beta is a machine of the group and also the operator: %v", err)
	}
	if _, err := settingsfile.Load(dir, "alpha", upEnv(t), false); err != nil {
		t.Errorf("the other machines are fine: %v", err)
	}
}

func TestTheOperatorSendsFromThisComputerAndTheMachineSeesTheOperator(t *testing.T) {
	dir, env := natsGroup(t, "")
	op := &fakeOp{registered: map[string]string{}}
	_, h := fakeTools(t)
	h.Env = func() (settings.Env, error) { return env, nil }
	h.Services.NATSOp = op
	if err := cli.RunCommand("nats", &cli.Opts{Host: h, Dir: dir, Args: []string{"init", "dev"}}); err != nil {
		t.Fatal(err)
	}
	// the server, with the operator as registered, and the machine beta as an agent would be
	tmp := t.TempDir()
	betaID, _ := natsserver.NewIdentity()
	betaSeed := filepath.Join(tmp, "beta.nk")
	if err := os.WriteFile(betaSeed, []byte(betaID.Seed+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	block, err := natsserver.AuthorizationBlock("dev", []natsserver.Client{
		{Machine: "beta", PublicKey: betaID.PublicKey}, {Machine: "operator", PublicKey: op.registered["operator"], Operator: true}})
	if err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(tmp, "server.conf")
	if err := os.WriteFile(conf, []byte("host: \"127.0.0.1\"\nport: -1\n\n"+block), 0o600); err != nil {
		t.Fatal(err)
	}
	opts, err := server.ProcessConfigFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("server not ready")
	}
	t.Cleanup(srv.Shutdown)
	beta := natsmcp.NewService(natsmcp.Config{URL: srv.ClientURL(), SeedFile: betaSeed, Names: natsmcp.Names{Group: "dev", Me: "beta"},
		PushFrom: map[string]bool{}, MaxText: natsmcp.DefaultMaxText, InboxMax: 50, InboxPerMan: 50, Quiet: true}, quietLog{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := beta.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(beta.Close)

	tunnels := 0
	h.OpenTunnel = func(_ context.Context, _ driving.NATSTarget, host string, port int) (string, func(), error) {
		tunnels++
		if host != "10.0.0.2" || port != 4222 {
			t.Errorf("the tunnel goes to the server of the group: %s:%d", host, port)
		}
		return strings.TrimPrefix(srv.ClientURL(), "nats://"), func() {}, nil
	}
	var out, errb strings.Builder
	h.Stdout, h.Stderr = &out, &errb
	err = cli.RunCommand("nats", &cli.Opts{Host: h, Dir: dir, Args: []string{"send", "dev", "hello from the operator"}, To: "beta"})
	if err != nil || !strings.Contains(errb.String(), "Sender: operator") || !strings.Contains(out.String(), "sent as operator to beta") || tunnels != 1 {
		t.Fatalf("%v tunnels %d\nstdout %s\nstderr %s", err, tunnels, out.String(), errb.String())
	}
	inbox := func() string {
		for _, tl := range beta.Tools() {
			if tl.Name == "inbox" {
				txt, _ := tl.Run(context.Background(), []byte("{}"))
				return txt
			}
		}
		return ""
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(inbox(), "hello from the operator") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	got := inbox()
	if !strings.Contains(got, "operator") || !strings.Contains(got, "hello from the operator") {
		t.Fatalf("beta got %q", got)
	}
}

func TestDownOfAGroupSaysThatTheOperatorKeyIsLeft(t *testing.T) {
	dir, env := natsGroup(t, "")
	_, h := fakeTools(t)
	downHost(h, downRoles(), func(string, func(string) string) (int, error) { return 0, nil })
	run := func() string {
		var out strings.Builder
		if err := cli.RunGroupDown(context.Background(), &cli.Opts{Host: h, Dir: dir, Group: "dev"}, env, strings.NewReader(""), &out, io.Discard); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if out := run(); strings.Contains(out, "operator") {
		t.Errorf("no key, no hint:\n%s", out)
	}
	key := operatorKey(t, dir, env)
	if err := os.MkdirAll(filepath.Dir(key), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("SU..\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := run(); !strings.Contains(out, "key of the operator") || !strings.Contains(out, key) {
		t.Errorf("a key that is left is named:\n%s", out)
	}
}
