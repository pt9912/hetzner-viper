package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/settingsfile"
	"github.com/pt9912/hetzner-viper/internal/adapter/driving/cli"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

const fakeCA = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"

// lockedBuf is a writer that may be read while another goroutine writes.
type lockedBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// tunnelRun is one viper nats tunnel dev: its streams, the tunnels it opened and closed, and the end.
type tunnelRun struct {
	out, errs      *lockedBuf
	stdin          *io.PipeWriter
	opened, closed int
	mu             sync.Mutex
	done           chan error
}

func (r *tunnelRun) counts() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.opened, r.closed
}

func startTunnel(t *testing.T, dir string, env settings.Env, op *fakeOp, p cli.Opts) *tunnelRun {
	t.Helper()
	_, h := fakeTools(t)
	h.Env = func() (settings.Env, error) { return env, nil }
	h.Services.NATSOp = op
	r := &tunnelRun{out: &lockedBuf{}, errs: &lockedBuf{}, done: make(chan error, 1)}
	in, w := io.Pipe()
	r.stdin = w
	h.Stdin, h.Stdout, h.Stderr = in, r.out, r.errs
	h.OpenTunnel = func(_ context.Context, _ driving.NATSTarget, host string, port int) (string, func(), error) {
		r.mu.Lock()
		r.opened++
		r.mu.Unlock()
		if host != "10.0.0.2" || port != 4222 {
			t.Errorf("the tunnel goes to the server of the group: %s:%d", host, port)
		}
		return "127.0.0.1:45678", func() { r.mu.Lock(); r.closed++; r.mu.Unlock() }, nil
	}
	p.Host = h
	switch p.Dir {
	case "":
		p.Dir = dir
	case noDir:
		p.Dir = ""
	}
	if p.Args == nil {
		p.Args = []string{"tunnel", "dev"}
	}
	go func() { r.done <- cli.RunCommand("nats", &p) }()
	t.Cleanup(func() { _ = w.Close() })
	return r
}

// ready waits for the line ready on stdout.
func (r *tunnelRun) ready(t *testing.T) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if lines := strings.Split(strings.TrimSpace(r.out.String()), "\n"); lines[0] != "" {
			var m map[string]any
			if err := json.Unmarshal([]byte(lines[0]), &m); err != nil {
				t.Fatalf("stdout is not JSON: %q", r.out.String())
			}
			return m
		}
		select {
		case err := <-r.done:
			t.Fatalf("ended before ready: %v\nstderr: %s", err, r.errs.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatalf("no line ready\nstderr: %s", r.errs.String())
	return nil
}

func (r *tunnelRun) end(t *testing.T) error {
	t.Helper()
	_ = r.stdin.Close()
	select {
	case err := <-r.done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("did not end after the end of stdin")
		return nil
	}
}

// withIdentity makes the key of local and registers it, as viper nats init --name local does, and returns the fake server.
func withIdentity(t *testing.T, dir string, env settings.Env) *fakeOp {
	t.Helper()
	op := &fakeOp{registered: map[string]string{}, ca: fakeCA}
	if _, err := identityRun(t, dir, env, op, "init", cli.Opts{Name: "local"}); err != nil {
		t.Fatal(err)
	}
	return op
}

func TestTunnelWritesOneLineReadyAndStaysUntilStdinEnds(t *testing.T) {
	dir, env := natsGroup(t, "")
	op := withIdentity(t, dir, env)
	r := startTunnel(t, dir, env, op, cli.Opts{})
	m := r.ready(t)
	if m["event"] != "ready" || m["group"] != "dev" || m["name"] != "local" || m["url"] != "tls://127.0.0.1:45678" || m["server_name"] != "10.0.0.2" {
		t.Errorf("%v", m)
	}
	abs, _ := filepath.Abs(dir)
	if m["settings_dir"] != abs {
		t.Errorf("settings_dir %v, want %s", m["settings_dir"], abs)
	}
	seed, _ := m["seed_file"].(string)
	if !strings.HasSuffix(filepath.ToSlash(seed), "/identities/local.nk") || !filepath.IsAbs(seed) {
		t.Errorf("seed_file %v", m["seed_file"])
	}
	ca, _ := m["ca_file"].(string)
	if b, err := os.ReadFile(ca); err != nil || string(b) != fakeCA || !filepath.IsAbs(ca) {
		t.Errorf("ca_file %q: %v", ca, err)
	}
	select {
	case err := <-r.done:
		t.Fatalf("it ended by itself: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := r.end(t); err != nil {
		t.Errorf("the end of stdin is an ordinary end: %v", err)
	}
	if o, c := r.counts(); o != 1 || c != 1 {
		t.Errorf("tunnels opened %d closed %d", o, c)
	}
	if _, err := os.Stat(ca); !os.IsNotExist(err) {
		t.Errorf("the certificate stays: %v", err)
	}
	if n := len(strings.Split(strings.TrimSpace(r.out.String()), "\n")); n != 1 {
		t.Errorf("stdout has %d lines: %q", n, r.out.String())
	}
	if !strings.Contains(r.errs.String(), "(--dir)") {
		t.Errorf("stderr says where the settings are: %q", r.errs.String())
	}
}

func TestTunnelRefusesAnIdentityTheServerDoesNotHaveOrHasWithAnotherKey(t *testing.T) {
	dir, env := natsGroup(t, "")
	op := withIdentity(t, dir, env)
	good := op.registered["local"]
	// the server lost the entry
	delete(op.registered, "local")
	r := startTunnel(t, dir, env, op, cli.Opts{})
	if err := <-r.done; err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Errorf("%v", err)
	}
	// the server has another key
	op.registered["local"] = "UOTHER"
	r = startTunnel(t, dir, env, op, cli.Opts{})
	if err := <-r.done; err == nil || !strings.Contains(err.Error(), "another key") {
		t.Errorf("%v", err)
	}
	// no key here
	op.registered["local"] = good
	r = startTunnel(t, dir, env, op, cli.Opts{Name: "other"})
	if err := <-r.done; err == nil || !strings.Contains(err.Error(), "no key on this computer") {
		t.Errorf("%v", err)
	}
	if r.out.String() != "" {
		t.Errorf("stdout must stay empty on a failure: %q", r.out.String())
	}
}

func TestTunnelNeedsYesWhenAMachinePushesTheNameIntoItsSession(t *testing.T) {
	dir, env := natsGroup(t, "NATS_MCP_PUSH_FROM=local\n")
	op := withIdentity(t, dir, env)
	r := startTunnel(t, dir, env, op, cli.Opts{})
	err := <-r.done
	var code interface{ Error() string }
	if !errors.As(err, &code) || err.Error() != "exit status 3" {
		t.Fatalf("%v", err)
	}
	for _, m := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(r.errs.String(), m) {
			t.Errorf("stderr names %s: %q", m, r.errs.String())
		}
	}
	if o, _ := r.counts(); o != 0 || r.out.String() != "" {
		t.Errorf("nothing is opened or written without --yes: %d %q", o, r.out.String())
	}
	r = startTunnel(t, dir, env, op, cli.Opts{Yes: true})
	r.ready(t)
	if err := r.end(t); err != nil {
		t.Error(err)
	}
}

// noDir is Opts.Dir for a run that is given no --dir.
const noDir = "-"

func TestTunnelFindsTheGroupFromAnotherFolderByTheFileInit(t *testing.T) {
	dir, env := natsGroup(t, "")
	op := withIdentity(t, dir, env) // init remembers the folder with the settings
	t.Chdir(t.TempDir())
	r := startTunnel(t, dir, env, op, cli.Opts{Dir: noDir})
	m := r.ready(t)
	abs, _ := filepath.Abs(dir)
	if m["settings_dir"] != abs || !strings.Contains(r.errs.String(), "remembered") {
		t.Errorf("%v\n%s", m, r.errs.String())
	}
	if err := r.end(t); err != nil {
		t.Error(err)
	}
	// the folder moved
	moved := dir + "-moved"
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	r = startTunnel(t, moved, env, op, cli.Opts{Dir: noDir})
	if err := <-r.done; err == nil || !strings.Contains(err.Error(), "moved?") {
		t.Errorf("%v", err)
	}
}

// homeAt is the environment with the home folder at home, the ssh configs of the machines, the identity local registered and the
// pointer of init removed: the group can be found by walking up only.
func homeAt(t *testing.T, dir string, env settings.Env, home string) (settings.Env, *fakeOp) {
	t.Helper()
	env.Home = home
	for _, m := range []string{"alpha", "beta", "gamma"} {
		c, err := settingsfile.Load(dir, m, env, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(c.SSHConfig), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(c.SSHConfig, []byte("Host x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	op := withIdentity(t, dir, env)
	if err := os.Remove(filepath.Join(home, ".local", "state", "viper", "groups", "dev", "settings-dir")); err != nil {
		t.Fatal(err)
	}
	return env, op
}

func TestTunnelLooksAboveTheCurrentFolderOnlyWithAKeyAndNeverAboveHome(t *testing.T) {
	dir, env0 := natsGroup(t, "")
	env, op := homeAt(t, dir, env0, filepath.Dir(dir)) // the settings are below the home folder
	deep := filepath.Join(dir, "project", "src")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(deep)
	r := startTunnel(t, dir, env, op, cli.Opts{Dir: noDir})
	r.ready(t)
	if !strings.Contains(r.errs.String(), "above the current one") {
		t.Errorf("stderr says which folder it took: %q", r.errs.String())
	}
	if err := r.end(t); err != nil {
		t.Error(err)
	}
	// no key of this name here: the folders above are not even looked at
	r = startTunnel(t, dir, env, op, cli.Opts{Dir: noDir, Name: "other"})
	if err := <-r.done; err == nil || !strings.Contains(err.Error(), "not looked at") || !strings.Contains(err.Error(), "--dir ABSOLUTE-FOLDER") {
		t.Errorf("%v", err)
	}
	// not above the home folder: here the settings are above it
	high, hop := homeAt(t, dir, env0, filepath.Join(dir, "project"))
	r = startTunnel(t, dir, high, hop, cli.Opts{Dir: noDir})
	if err := <-r.done; err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Errorf("%v", err)
	}
}

func TestTunnelDirMustHoldTheGroup(t *testing.T) {
	dir, env := natsGroup(t, "")
	op := withIdentity(t, dir, env)
	r := startTunnel(t, dir, env, op, cli.Opts{Dir: t.TempDir()})
	if err := <-r.done; err == nil || !strings.Contains(err.Error(), "has no groups/dev.env") {
		t.Errorf("%v", err)
	}
	r = startTunnel(t, dir, env, op, cli.Opts{Args: []string{"tunnel", "-x"}})
	if err := <-r.done; err == nil {
		t.Error("a group name that starts with a dash is refused")
	}
}

func TestTunnelPortIsReadByTheParserAndOnlyTunnelTakesIt(t *testing.T) {
	_, o, err := cli.Parse([]string{"nats", "tunnel", "dev", "--port", "40001", "--name", "x", "--yes"})
	if err != nil || o.Port != 40001 || o.Name != "x" || !o.Yes {
		t.Errorf("%v %+v", err, o)
	}
	dir, env := natsGroup(t, "")
	_, h := fakeTools(t)
	h.Env = func() (settings.Env, error) { return env, nil }
	if err := cli.RunCommand("nats", &cli.Opts{Host: h, Dir: dir, Args: []string{"peers", "dev"}, Port: 40001}); err == nil || !strings.Contains(err.Error(), "port") {
		t.Errorf("peers does not take --port: %v", err)
	}
}

func TestPrintConfigWritesTheEntryForClaudeCodeAndWhatIsMissing(t *testing.T) {
	dir, env := natsGroup(t, "NATS_MCP_PUSH_FROM=local\n")
	_, h := fakeTools(t)
	h.Env = func() (settings.Env, error) { return env, nil }
	var out lockedBuf
	h.Stdout = &out
	link := filepath.Join(t.TempDir(), "brew", "bin", "viper") // a link in the folder of the package manager, not the folder of versions behind it
	h.Services.Executable = func() (string, error) { return link, nil }
	serverName := "viper-nats-mcp"
	if runtime.GOOS == "windows" {
		serverName += ".exe"
	}
	server := filepath.Join(filepath.Dir(link), serverName)
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(server, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := cli.RunCommand("nats", &cli.Opts{Host: h, Dir: dir, Args: []string{"tunnel", "dev"}, PrintConfig: true, PushFrom: "beta, gamma", Files: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	abs, _ := filepath.Abs(dir)
	for _, want := range []string{"claude mcp add nats-dev -- ", "--via-viper dev", "--dir ", abs, "--push-from ", "beta, gamma", "--files ", `"mcpServers"`, `"nats-dev"`,
		"--dangerously-load-development-channels server:nats-dev", "no key for local: run viper nats init dev --name local", "local is in NATS_MCP_PUSH_FROM of alpha, beta, gamma"} {
		if !strings.Contains(got, want) {
			t.Errorf("the output lacks %q:\n%s", want, got)
		}
	}
	// the entry names viper and the server under the path viper was started with, in the folder of that path
	has := func(before, p, after string) bool {
		for _, q := range []string{"", `"`, "'"} {
			if strings.Contains(got, before+q+p+q+after) {
				return true
			}
		}
		return false
	}
	if !has("--viper ", link, " ") || !has("-- ", server, " --via-viper") || strings.Contains(got, "program is not there yet") {
		t.Errorf("viper and the server are named by the path of the call (%s), the server is there:\n%s", link, got)
	}
	if strings.Contains(got, "--yes") && !strings.Contains(got, "no --yes") {
		t.Errorf("the entry must not carry --yes:\n%s", got)
	}
	if err := os.Remove(server); err != nil {
		t.Fatal(err)
	}
	out.mu.Lock()
	out.b.Reset()
	out.mu.Unlock()
	if err := cli.RunCommand("nats", &cli.Opts{Host: h, Dir: dir, Args: []string{"tunnel", "dev"}, PrintConfig: true}); err != nil || !strings.Contains(out.String(), "the program is not there yet: run viper mcp-binary --host") {
		t.Errorf("a missing server is said: %v\n%s", err, out.String())
	}
	err = cli.RunCommand("nats", &cli.Opts{Host: h, Dir: dir, Args: []string{"tunnel", "dev"}, PushFrom: "beta"})
	if err == nil || !strings.Contains(err.Error(), "--print-config") {
		t.Errorf("--push-from without --print-config: %v", err)
	}
}

func TestPrintConfigFindsTheGroupInTheCurrentFolderLikeEveryOtherCommand(t *testing.T) {
	dir, env := natsGroup(t, "")
	_, h := fakeTools(t)
	h.Env = func() (settings.Env, error) { return env, nil }
	var out lockedBuf
	h.Stdout = &out
	t.Chdir(dir) // no --dir, no key, nothing remembered
	if err := cli.RunCommand("nats", &cli.Opts{Host: h, Args: []string{"tunnel", "dev"}, PrintConfig: true}); err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(dir)
	for _, want := range []string{"the current folder", "--dir ", abs, "run viper nats init dev --name local"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the output lacks %q:\n%s", want, out.String())
		}
	}
	// the tunnel itself, started by a program from a project folder, does not trust the current folder
	t.Chdir(t.TempDir())
	r := startTunnel(t, dir, env, &fakeOp{registered: map[string]string{}}, cli.Opts{Dir: noDir})
	if err := <-r.done; err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Errorf("%v", err)
	}
}
