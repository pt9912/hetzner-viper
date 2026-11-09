package cli_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/cli"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/runlog"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

func TestGroupOptionsAreParsedAndExcludeAMachine(t *testing.T) {
	name, o, err := cli.Parse([]string{"check", "--group", "dev", "--parallel", "3"})
	if err != nil || name != "check" || o.Group != "dev" || o.Parallel != 3 || o.Machine != "" {
		t.Fatalf("%v %+v", err, o)
	}
	if _, _, err := cli.Parse([]string{"ssh", "--group=dev", "--cmd", "uptime"}); err != nil {
		t.Error(err)
	}
	for _, args := range [][]string{
		{"check", "web", "--group", "dev"},
		{"check", "--parallel", "0", "--group", "dev"},
		{"check", "--parallel", "x", "--group", "dev"},
		{"check"},
		{"find", "--group", "dev"},
	} {
		if _, _, err := cli.Parse(args); err == nil {
			t.Errorf("%v must be refused", args)
		}
	}
}

func TestThePrefixWriterWritesWholeLinesWithTheNameOfTheMachine(t *testing.T) {
	var out bytes.Buffer
	ws, flush := cli.NewPrefixWriters(&out, 4, "a", "beta")
	// the machines say their lines in pieces, at the same time
	fmt.Fprint(ws[0], "hel")
	fmt.Fprint(ws[1], "one\ntw")
	fmt.Fprint(ws[0], "lo\n\n")
	fmt.Fprint(ws[1], "o\r50%\r100%\r\nrest")
	flush()
	want := []string{"[beta] one", "[a]    hello", "[beta] two", "[beta] 50%", "[beta] 100%", "[beta] rest"}
	for _, w := range want {
		if !strings.Contains(out.String(), w+"\n") {
			t.Errorf("missing %q in\n%s", w, out.String())
		}
	}
	if strings.Contains(out.String(), "\n\n") || strings.Contains(out.String(), "\r") {
		t.Errorf("empty lines and returns are dropped:\n%q", out.String())
	}
}

func groupDir(t *testing.T, machines string) (string, settings.Env) {
	t.Helper()
	env, _ := nativeEnv(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "groups"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "groups", "dev.env"), []byte("GROUP_MACHINES="+machines+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, env
}

func TestAGroupCommandRunsForEveryMachineWithPrefixesAndPrintsTheTable(t *testing.T) {
	dir, env := groupDir(t, "alpha,beta,gamma")
	_, h := fakeTools(t)
	var mu sync.Mutex
	var ran []string
	var out, errb bytes.Buffer
	err := cli.RunGroup(context.Background(), &cli.Opts{Host: h, Dir: dir, Group: "dev"}, env, &out, &errb,
		func(_ context.Context, p *cli.Opts, _ settings.Env, o, e io.Writer) error {
			mu.Lock()
			ran = append(ran, p.Machine)
			mu.Unlock()
			fmt.Fprintf(o, "hello from %s\n", p.Machine)
			switch p.Machine {
			case "beta":
				fmt.Fprintln(e, "beta is unhappy")
				return cli.ExitStatus(1)
			case "gamma":
				return errors.New("no SSH config")
			}
			return nil
		})
	if cli.ExitCode(err) != 1 {
		t.Fatalf("a failed machine: exit status 1, got %v", err)
	}
	if len(ran) != 3 {
		t.Errorf("ran for %v", ran)
	}
	for _, want := range []string{"[alpha] hello from alpha\n", "[beta]  hello from beta\n", "[gamma] hello from gamma\n", "== Result", "alpha  ok", "beta   failed (exit status 1", "gamma  failed: no SSH config"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in\n%s", want, out.String())
		}
	}
	if !strings.Contains(errb.String(), "[beta]  beta is unhappy\n") || !strings.Contains(errb.String(), "[gamma] no SSH config\n") {
		t.Errorf("stderr:\n%s", errb.String())
	}
}

func TestAGroupCommandForAGroupThatDoesNotExistSaysSo(t *testing.T) {
	dir, env := groupDir(t, "alpha")
	_, h := fakeTools(t)
	err := cli.RunGroup(context.Background(), &cli.Opts{Host: h, Dir: dir, Group: "nope"}, env, io.Discard, io.Discard,
		func(context.Context, *cli.Opts, settings.Env, io.Writer, io.Writer) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "No group nope") {
		t.Errorf("%v", err)
	}
}

func TestSSHForAGroupNeedsACommand(t *testing.T) {
	dir, env := groupDir(t, "alpha")
	_ = env
	_, h := fakeTools(t)
	for _, p := range []*cli.Opts{{Host: h, Dir: dir, Group: "dev"}, {Host: h, Dir: dir, Group: "dev", Cmd: "x", Tmux: true}} {
		if err := cli.RunCommand("ssh", p); err == nil || !strings.Contains(err.Error(), "needs --cmd") {
			t.Errorf("%v", err)
		}
	}
}

// upFake is viper up of the machines of a group: what each does is set by its name.
type upFake struct {
	mu      sync.Mutex
	started []string
	signals map[string]bool
	opts    map[string]driving.UpOptions
	script  func(name string, o driving.UpOptions) (int, error)
}

func (f *upFake) services(h *cli.Host) {
	f.signals, f.opts = map[string]bool{}, map[string]driving.UpOptions{}
	h.Services.Up = func(c *model.Machine, o driving.UpOptions) driving.Up {
		return upFunc(func(ctx context.Context) (int, error) {
			f.mu.Lock()
			f.started = append(f.started, c.Name)
			f.opts[c.Name] = o
			f.mu.Unlock()
			return f.script(c.Name, o)
		})
	}
	h.Services.Preflight = func(c *model.Machine) driving.Preflight { return preflightFunc(func() int { return 0 }) }
}

type upFunc func(ctx context.Context) (int, error)

func (u upFunc) Up(ctx context.Context, _ *model.Machine, _ string, _ runlog.Log, _, _ io.Writer) (int, error) {
	return u(ctx)
}

type preflightFunc func() int

func (p preflightFunc) Preflight(context.Context, *model.Machine, string, io.Writer, io.Writer) int {
	return p()
}

func upEnv(t *testing.T) settings.Env {
	t.Helper()
	env, _ := nativeEnv(t)
	env.Lookup = func(k string) (string, bool) {
		v, ok := map[string]string{"HOME": env.Home, "HCLOUD_TOKEN": "h"}[k]
		return v, ok
	}
	return env
}

func upGroupDir(t *testing.T, machines, extra string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, text string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".env.local", "DNS_DOMAIN=example.com\nDNS_PROVIDER=none\n")
	write("groups/dev.env", "GROUP_MACHINES="+machines+"\nNATS_ENABLED=true\n"+extra)
	return dir
}

func TestTheClientsOfANewGroupStartOnlyAfterTheServerExists(t *testing.T) {
	dir := upGroupDir(t, "alpha,beta,gamma", "")
	_, h := fakeTools(t)
	f := &upFake{}
	f.services(h)
	var serverSignaled atomic.Bool
	var early atomic.Int32
	f.script = func(name string, o driving.UpOptions) (int, error) {
		if name == "alpha" {
			time.Sleep(50 * time.Millisecond)
			serverSignaled.Store(true)
			o.OnServerCreated()
			time.Sleep(50 * time.Millisecond)
			return 0, nil
		}
		if !serverSignaled.Load() {
			early.Add(1)
		}
		return 0, nil
	}
	var out, errb bytes.Buffer
	err := cli.RunGroupUp(context.Background(), &cli.Opts{Host: h, Dir: dir, Group: "dev", NoJitter: true}, upEnv(t), &out, &errb)
	if err != nil {
		t.Fatalf("%v\n%s\n%s", err, out.String(), errb.String())
	}
	if early.Load() != 0 || len(f.started) != 3 || f.started[0] != "alpha" {
		t.Errorf("started %v, %d before the server", f.started, early.Load())
	}
	for _, m := range []string{"alpha", "beta", "gamma"} {
		o := f.opts[m]
		if !o.NoCode || !o.SkipPreflight || o.ServerWait != 15*time.Minute || o.OnServerCreated == nil {
			t.Errorf("%s: %+v", m, o)
		}
	}
	for _, want := range []string{"== Result", "alpha  ok", "beta   ok", "gamma  ok"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in\n%s", want, out.String())
		}
	}
}

func TestNothingIsMadeIfTheChecksOfOneMachineFail(t *testing.T) {
	dir := upGroupDir(t, "alpha,beta", "")
	_, h := fakeTools(t)
	f := &upFake{script: func(string, driving.UpOptions) (int, error) { return 0, nil }}
	f.services(h)
	h.Services.Preflight = func(c *model.Machine) driving.Preflight {
		return preflightFunc(func() int {
			if c.Name == "beta" {
				return 1
			}
			return 0
		})
	}
	var out, errb bytes.Buffer
	err := cli.RunGroupUp(context.Background(), &cli.Opts{Host: h, Dir: dir, Group: "dev", NoJitter: true}, upEnv(t), &out, &errb)
	if cli.ExitCode(err) == 0 || len(f.started) != 0 {
		t.Fatalf("%v, started %v", err, f.started)
	}
	if !strings.Contains(errb.String(), "Nothing was made") {
		t.Errorf("stderr:\n%s", errb.String())
	}
}

func TestIfTheServerFailsBeforeItExistsNoClientIsStarted(t *testing.T) {
	dir := upGroupDir(t, "alpha,beta,gamma", "")
	_, h := fakeTools(t)
	f := &upFake{script: func(name string, _ driving.UpOptions) (int, error) { return 1, driving.ErrShown }}
	f.services(h)
	var out, errb bytes.Buffer
	err := cli.RunGroupUp(context.Background(), &cli.Opts{Host: h, Dir: dir, Group: "dev", NoJitter: true}, upEnv(t), &out, &errb)
	if cli.ExitCode(err) != 1 || len(f.started) != 1 || f.started[0] != "alpha" {
		t.Fatalf("%v started %v", err, f.started)
	}
	for _, want := range []string{"alpha  failed", "beta   failed: skipped: alpha failed before its server existed", "gamma  failed: skipped: alpha failed"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in\n%s", want, out.String())
		}
	}
}

func TestIfTheServerFailsAfterItExistsTheClientsAreStopped(t *testing.T) {
	dir := upGroupDir(t, "alpha,beta", "")
	_, h := fakeTools(t)
	betaRunning := make(chan struct{})
	h.Services.Preflight = func(c *model.Machine) driving.Preflight { return preflightFunc(func() int { return 0 }) }
	h.Services.Up = func(c *model.Machine, o driving.UpOptions) driving.Up {
		return upFunc(func(ctx context.Context) (int, error) {
			if c.Name == "beta" {
				close(betaRunning)
				<-ctx.Done()
				return 1, ctx.Err()
			}
			o.OnServerCreated()
			<-betaRunning
			return 1, driving.ErrShown
		})
	}
	var out, errb bytes.Buffer
	start := time.Now()
	err := cli.RunGroupUp(context.Background(), &cli.Opts{Host: h, Dir: dir, Group: "dev", NoJitter: true}, upEnv(t), &out, &errb)
	if cli.ExitCode(err) != 1 || time.Since(start) > 3*time.Second {
		t.Fatalf("%v after %v\n%s", err, time.Since(start), out.String())
	}
	if !strings.Contains(out.String(), "beta   failed: stopped because the server alpha failed") {
		t.Errorf("table:\n%s", out.String())
	}
}

func TestMachinesWithOtherKeysAndFirewallsWaitForTheirFirstMachine(t *testing.T) {
	dir := upGroupDir(t, "alpha,beta,gamma", "")
	if err := os.MkdirAll(filepath.Join(dir, "machines"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "machines", "gamma.env.local"), []byte("SERVER_PREFIX=other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, h := fakeTools(t)
	f := &upFake{}
	f.services(h)
	var order []string
	var omu sync.Mutex
	h.Services.Up = func(c *model.Machine, o driving.UpOptions) driving.Up {
		return upFunc(func(context.Context) (int, error) {
			omu.Lock()
			order = append(order, c.Name)
			omu.Unlock()
			o.OnServerCreated()
			time.Sleep(30 * time.Millisecond)
			return 0, nil
		})
	}
	var out, errb bytes.Buffer
	if err := cli.RunGroupUp(context.Background(), &cli.Opts{Host: h, Dir: dir, Group: "dev", NoJitter: true}, upEnv(t), &out, &errb); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	// alpha is the server and the first of the prefix claude; gamma is the first of the prefix other
	// but still a client of the group: it waits for the server
	if len(order) != 3 || order[0] != "alpha" {
		t.Errorf("order %v", order)
	}
}

// downFake is viper down of a machine of a group.
type downFake struct {
	name string
	role model.NATSRole
	ask  func(string) string
	run  func(name string, ask func(string) string) (int, error)
}

func (d downFake) Down(_ context.Context, _ *model.Machine, _ bool, _ runlog.Log, _, _ io.Writer) (int, error) {
	return d.run(d.name, d.ask)
}

func (d downFake) GroupRole(context.Context, *model.Machine) (model.NATSRole, string, bool, error) {
	return d.role, "dev", true, nil
}

func downHost(h *cli.Host, roles map[string]model.NATSRole, run func(name string, ask func(string) string) (int, error)) {
	h.Services.Down = func(c *model.Machine, o driving.DownOptions) driving.Down {
		return downFake{name: c.Name, role: roles[c.Name], ask: o.Ask, run: run}
	}
}

func downRoles() map[string]model.NATSRole {
	return map[string]model.NATSRole{"alpha": model.NATSServer, "beta": model.NATSClient, "gamma": model.NATSClient}
}

func TestTheServerOfAGroupIsDeletedLastAndTheClientsTogether(t *testing.T) {
	dir := upGroupDir(t, "alpha,beta,gamma", "")
	_, h := fakeTools(t)
	var mu sync.Mutex
	var order []string
	var running, peak int
	downHost(h, downRoles(), func(name string, ask func(string) string) (int, error) {
		mu.Lock()
		running++
		peak = max(peak, running)
		mu.Unlock()
		time.Sleep(60 * time.Millisecond)
		mu.Lock()
		running--
		order = append(order, name)
		mu.Unlock()
		return 0, nil
	})
	var out, errb bytes.Buffer
	err := cli.RunGroupDown(context.Background(), &cli.Opts{Host: h, Dir: dir, Group: "dev"}, upEnv(t), strings.NewReader(""), &out, &errb)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(order) != 3 || order[2] != "alpha" || peak != 2 {
		t.Errorf("order %v, %d at the same time", order, peak)
	}
}

func TestTheServerStaysIfAClientIsNotDeleted(t *testing.T) {
	dir := upGroupDir(t, "alpha,beta,gamma", "")
	_, h := fakeTools(t)
	var mu sync.Mutex
	var ran []string
	downHost(h, downRoles(), func(name string, ask func(string) string) (int, error) {
		mu.Lock()
		ran = append(ran, name)
		mu.Unlock()
		if name == "beta" {
			return 1, nil // DISCARD refused, say
		}
		return 0, nil
	})
	var out, errb bytes.Buffer
	err := cli.RunGroupDown(context.Background(), &cli.Opts{Host: h, Dir: dir, Group: "dev"}, upEnv(t), strings.NewReader(""), &out, &errb)
	if cli.ExitCode(err) != 1 || slices.Contains(ran, "alpha") {
		t.Fatalf("%v ran %v", err, ran)
	}
	for _, want := range []string{"alpha  skipped: the server stays: beta not deleted", "beta   failed", "gamma  ok"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in\n%s", want, out.String())
		}
	}
}

func TestTheMachinesOfAGroupAskOneAfterTheOtherWithTheirName(t *testing.T) {
	dir := upGroupDir(t, "alpha,beta,gamma", "")
	_, h := fakeTools(t)
	downHost(h, downRoles(), func(name string, ask func(string) string) (int, error) {
		// the real question comes in the middle of the run; the machines ask at the same time
		got := ask("Type DELETE to continue: ")
		if got != "DELETE" {
			return 1, nil
		}
		return 0, nil
	})
	var out, errb bytes.Buffer
	err := cli.RunGroupDown(context.Background(), &cli.Opts{Host: h, Dir: dir, Group: "dev"}, upEnv(t), strings.NewReader("DELETE\nDELETE\nDELETE\n"), &out, &errb)
	if err != nil {
		t.Fatalf("%v\n%s\n%s", err, out.String(), errb.String())
	}
	for _, m := range []string{"[alpha] Type DELETE", "[beta] Type DELETE", "[gamma] Type DELETE"} {
		if !strings.Contains(errb.String(), m) {
			t.Errorf("missing %q in\n%s", m, errb.String())
		}
	}
}

func TestSetupCreatesTheGroupExampleAndKeepsAnExistingOne(t *testing.T) {
	env, _ := nativeEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, h := fakeTools(t)
	var out bytes.Buffer
	if err := cli.Setup(&cli.Opts{Host: h, Dir: dir}, env, &out); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "groups", "example.env.example")
	b, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(b), "GROUP_MACHINES=") || !strings.Contains(out.String(), "groups/<group>.env") {
		t.Fatalf("%v %q", err, out.String())
	}
	if err := os.WriteFile(file, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cli.Setup(&cli.Opts{Host: h, Dir: dir}, env, io.Discard); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(file); string(b) != "mine\n" {
		t.Error("an existing example is not overwritten")
	}
}
