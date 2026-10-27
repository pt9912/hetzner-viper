package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/hetzner"
	"github.com/pt9912/hetzner-viper/internal/config"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/runlog"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
	"github.com/pt9912/hetzner-viper/internal/servertest"
	"github.com/pt9912/hetzner-viper/internal/usecase/check"
	"github.com/pt9912/hetzner-viper/internal/usecase/preflight"
	"github.com/pt9912/hetzner-viper/internal/usecase/start"
	"github.com/pt9912/hetzner-viper/internal/usecase/stop"
)

type okHetzner struct{}

func (okHetzner) ServerNames(context.Context) ([]string, error) { return nil, nil }
func (okHetzner) ServerType(context.Context, string) (*hetzner.ServerType, error) {
	return &hetzner.ServerType{Cores: 4, MemoryGB: 8, DiskGB: 160, Architecture: "x86", Locations: []hetzner.ServerTypeLocation{{Name: "fsn1", Available: true}}}, nil
}
func (okHetzner) LocationExists(context.Context, string) (bool, error)      { return true, nil }
func (okHetzner) ImageExists(context.Context, string, string) (bool, error) { return true, nil }
func (okHetzner) SSHKeys(context.Context) ([]hetzner.SSHKey, error)         { return nil, nil }

type okDNS struct{ ok bool }

func (d okDNS) Check(context.Context, string, string) ([]string, bool, error) {
	if !d.ok {
		return []string{"FAIL  x already has a record not managed by this setup"}, false, nil
	}
	return []string{"ok    zone"}, true, nil
}

func preflightSetup(t *testing.T, dnsOK bool) (config.Env, string, *options) {
	t.Helper()
	env, home := nativeEnv(t)
	t.Setenv("X", "")
	envMap := map[string]string{"HOME": home, "HCLOUD_TOKEN": "h", "CLOUDFLARE_API_TOKEN": "c", "CLOUDFLARE_ZONE_ID": "z"}
	env.Lookup = func(k string) (string, bool) { v, ok := envMap[k]; return v, ok }
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ssh := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"hetzner-build", "hetzner-build.pub", "hetzner_claude_host_ca"} {
		if err := os.WriteFile(filepath.Join(ssh, f), []byte("ssh-ed25519 AAAA k\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(ssh, "known_hosts_hetzner_claude_ca"), []byte("@cert-authority *.example.com AAAA\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := testHost()
	h.services.Preflight = func(*model.Machine) driving.Preflight {
		return preflight.Service{Deps: preflight.Deps{Cloud: hetzner.Validator{API: okHetzner{}}, DNS: okDNS{ok: dnsOK}}}
	}
	return env, home, &options{dir: dir, machine: "web", h: h}
}

func TestPreflightLogsTheRunAndItsOutcome(t *testing.T) {
	env, home, o := preflightSetup(t, true)
	var out, errb bytes.Buffer
	if err := runPreflight(context.Background(), o, env, &out, &errb); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Preflight OK.") {
		t.Errorf("output %q", out.String())
	}
	state := filepath.Join(home, ".local", "state", "hetzner-claude")
	events, _ := os.ReadFile(filepath.Join(state, "events.log"))
	if !strings.HasSuffix(string(events), " claude-web               preflight preflight OK\n") {
		t.Errorf("events %q", events)
	}
	log, _ := os.ReadFile(filepath.Join(state, "logs", "claude-web.log"))
	if !strings.Contains(string(log), " preflight claude-web\n") || !strings.Contains(string(log), "Preflight OK.") {
		t.Errorf("transcript %q", log)
	}
}

func TestPreflightProblemsGiveExitStatus1(t *testing.T) {
	env, home, o := preflightSetup(t, false)
	var out, errb bytes.Buffer
	err := runPreflight(context.Background(), o, env, &out, &errb)
	if exitCode(err) != 1 || !strings.Contains(out.String(), "Preflight: 1 problem(s); nothing was created.") {
		t.Fatalf("%v / %q", err, out.String())
	}
	events, _ := os.ReadFile(filepath.Join(home, ".local", "state", "hetzner-claude", "events.log"))
	if !strings.HasSuffix(string(events), "preflight preflight: 1 problem(s)\n") || strings.Contains(string(events), "FAILED") {
		t.Errorf("events %q", events)
	}
}

func TestPreflightNeedsTheSettingsBeforeAnything(t *testing.T) {
	env, home, o := preflightSetup(t, true)
	env.Lookup = func(k string) (string, bool) { return map[string]string{"HOME": home}[k], k == "HOME" }
	var out, errb bytes.Buffer
	err := runPreflight(context.Background(), o, env, &out, &errb)
	if err == nil || !strings.Contains(err.Error(), "Missing settings: HCLOUD_TOKEN CLOUDFLARE_API_TOKEN CLOUDFLARE_ZONE_ID") {
		t.Errorf("got %v", err)
	}
	if _, serr := os.Stat(filepath.Join(home, ".local", "state", "hetzner-claude")); serr == nil {
		t.Error("nothing is logged for a run that did not start")
	}
}

func TestPreflightRunsHereByDefault(t *testing.T) {
	if commands()["preflight"].run == nil {
		t.Error("preflight is native")
	}
	for _, name := range []string{"up", "down", "check", "log", "setup", "ssh", "dns"} {
		if commands()[name].run == nil {
			t.Errorf("%s is native", name)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type serverHetzner struct {
	hetzner.Identity
	srv *model.Server
}

func (s serverHetzner) ServerByName(context.Context, string) (*model.Server, error) {
	return s.srv, nil
}

func TestCheckLogsTheRunAndItsOutcome(t *testing.T) {
	env, home, o := preflightSetup(t, true)
	state := filepath.Join(home, ".local", "state", "hetzner-claude")
	var probeOK = true
	o.host().services.Check = func(c *model.Machine) driving.Check {
		return check.Service{Deps: check.Deps{
			Cloud: serverHetzner{srv: &model.Server{Name: "claude-web", IPv4: "203.0.113.7"}},
			HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"Answer":[{"type":1,"data":"203.0.113.7"}]}`))}, nil
			})},
			LookupIPv4: func(context.Context, string) ([]string, error) { return []string{"203.0.113.7"}, nil },
			SSH:        servertest.SSH{},
			Run: func(_ context.Context, _, name string, args ...string) (string, string, error) {
				if name == "ssh" && args[0] != "-n" && !probeOK {
					return "", "", errors.New("exit 255")
				}
				if name == "ssh" && args[0] != "-n" {
					return "cloudinit=done\nclaude_version=2\ngh_version=gh 2\ngit_name=a\ngit_email=b@c\ndocker=ok\nbrew_version=Homebrew 5\nbrew_path=yes\nsshd=authenticationmethods publickey;passwordauthentication no;permitrootlogin no;\n", "", nil
				}
				return "", "", nil
			},
		}}
	}
	var out, errb bytes.Buffer
	if err := runCheck(context.Background(), o, env, &out, &errb); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Check OK.") {
		t.Errorf("output %q", out.String())
	}
	events, _ := os.ReadFile(filepath.Join(state, "events.log"))
	if !strings.HasSuffix(string(events), " claude-web               check     check OK\n") {
		t.Errorf("events %q", events)
	}
	// an ssh failure: logged as FAILED, exit status 1
	probeOK = false
	err := runCheck(context.Background(), o, env, &out, &errb)
	events, _ = os.ReadFile(filepath.Join(state, "events.log"))
	if exitCode(err) != 1 || !strings.HasSuffix(string(events), "check     FAILED (exit 1; see logs/claude-web.log)\n") {
		t.Errorf("%v / events %q", err, events)
	}
	if commands()["check"].run == nil {
		t.Error("check is native")
	}
}

type downHetzner struct {
	hetzner.Identity
	srv     *model.Server
	deleted *[]string
}

func (d downHetzner) ServerByName(context.Context, string) (*model.Server, error) {
	return d.srv, nil
}
func (d downHetzner) DeleteServer(_ context.Context, id string) error {
	*d.deleted = append(*d.deleted, id)
	return nil
}

type dnsDeleter struct{}

func (dnsDeleter) Delete(context.Context, string, string) (string, error) {
	return "DNS record removed (if present)", nil
}

func TestDownReadsItsAnswersFromAPipeAndLogsTheOutcome(t *testing.T) {
	env, home, o := preflightSetup(t, true)
	state := filepath.Join(home, ".local", "state", "hetzner-claude")
	var deleted []string
	o.host().services.Down = func(c *model.Machine, do driving.DownOptions) driving.Down {
		return stop.Service{Deps: stop.Deps{
			Ask:   do.Ask,
			Cloud: downHetzner{srv: &model.Server{ID: "42", Name: "claude-web", IPv4: "203.0.113.7"}, deleted: &deleted},
			DNS:   dnsDeleter{},
			Run: func(_ context.Context, _, name string, args ...string) (string, string, error) {
				if strings.HasPrefix(args[len(args)-1], "bash -s") {
					return "== /Development ==\n== tmux sessions ==\nUNSAVED WORK: x\n== end of check ==\n", "", nil
				}
				return "ssh-ed25519 AAAA k\n", "", nil
			},
			SSH: servertest.SSH{},
		}}
	}
	// both answers follow each other in one pipe
	var out, errb bytes.Buffer
	err := runDown(context.Background(), o, env, strings.NewReader("DISCARD\nDELETE\n"), &out, &errb)
	if err != nil || len(deleted) != 1 || deleted[0] != "42" {
		t.Fatalf("%v deleted %v\n%s%s", err, deleted, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "Type DISCARD to continue: ") || !strings.Contains(errb.String(), "Type DELETE to continue: ") || !strings.Contains(out.String(), "Server claude-web deleted.") {
		t.Errorf("out %q err %q", out.String(), errb.String())
	}
	events, _ := os.ReadFile(filepath.Join(state, "events.log"))
	if !strings.Contains(string(events), "server deleted id=42 ip=203.0.113.7") || !strings.HasSuffix(string(events), " claude-web               down      deleted\n") {
		t.Errorf("events %q", events)
	}
	// the end of the input is no confirmation
	deleted = nil
	out.Reset()
	errb.Reset()
	err = runDown(context.Background(), o, env, strings.NewReader("DISCARD\n"), &out, &errb)
	if exitCode(err) != 1 || len(deleted) != 0 {
		t.Errorf("%v deleted %v", err, deleted)
	}
	events, _ = os.ReadFile(filepath.Join(state, "events.log"))
	if !strings.HasSuffix(string(events), "down      cancelled (no DELETE)\n") {
		t.Errorf("events %q", events)
	}
	if commands()["down"].run == nil {
		t.Error("down is native")
	}
}

func TestUpStopsAtPreflightProblemsAndLogsThem(t *testing.T) {
	env, home, o := preflightSetup(t, false) // the DNS check fails: one problem
	created := false
	o.host().services.Up = func(c *model.Machine, _ driving.UpOptions) driving.Up {
		return start.Service{Deps: start.Deps{
			Preflight: func(ctx context.Context, repoArg string, out, errOut io.Writer) int {
				return o.host().services.Preflight(c).Preflight(ctx, c, repoArg, out, errOut)
			},
			Cloud: failIfCreated{created: &created},
		}}
	}
	var out, errb bytes.Buffer
	err := runUp(context.Background(), o, env, &out, &errb)
	if exitCode(err) != 1 || created {
		t.Fatalf("%v created=%v\n%s%s", err, created, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "Preflight: 1 problem(s); nothing was created.") {
		t.Errorf("err %q", errb.String())
	}
	events, _ := os.ReadFile(filepath.Join(home, ".local", "state", "hetzner-claude", "events.log"))
	if !strings.Contains(string(events), " up        start (cpx32, fsn1, ubuntu-26.04)\n") || !strings.HasSuffix(string(events), "up        FAILED (exit 1; see logs/claude-web.log)\n") {
		t.Errorf("events %q", events)
	}
}

type failIfCreated struct {
	hetzner.Identity
	created *bool
}

func (f failIfCreated) ServerByName(context.Context, string) (*model.Server, error) {
	return nil, nil
}
func (f failIfCreated) EnsureLoginKey(context.Context, string, string) (model.LoginKey, error) {
	*f.created = true
	return model.LoginKey{Ref: "k"}, nil
}
func (f failIfCreated) EnsureFirewall(context.Context, string, []string) (model.FirewallResult, error) {
	*f.created = true
	return model.FirewallResult{}, nil
}
func (f failIfCreated) CreateServer(context.Context, model.ServerSpec) (*model.Server, error) {
	*f.created = true
	return nil, nil
}

func TestInterruptionAtAPromptIsLoggedNotAFailure(t *testing.T) {
	env, home, o := preflightSetup(t, true)
	state := filepath.Join(home, ".local", "state", "hetzner-claude")
	var deleted []string
	o.host().services.Down = func(c *model.Machine, do driving.DownOptions) driving.Down {
		return stop.Service{Deps: stop.Deps{
			Ask:   do.Ask,
			Cloud: downHetzner{srv: &model.Server{ID: "42", Name: "claude-web", IPv4: "203.0.113.7"}, deleted: &deleted},
			DNS:   dnsDeleter{},
			Run: func(_ context.Context, _, _ string, args ...string) (string, string, error) {
				if strings.HasPrefix(args[len(args)-1], "bash -s") {
					return "== /Development ==\n== tmux sessions ==\n== end of check ==\n", "", nil
				}
				return "ssh-ed25519 AAAA k\n", "", nil
			},
			SSH: servertest.SSH{},
		}}
	}
	pr, pw := io.Pipe()               // nothing is typed: the prompt waits
	defer func() { _ = pw.Close() }() // only unblocks the prompt reader
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var out, errb syncBuffer
	go func() { done <- runDown(ctx, o, env, pr, &out, &errb) }()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(errb.String(), "Type DELETE to continue: ") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if exitCode(err) != 130 {
			t.Errorf("exit code %d (%v)", exitCode(err), err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the prompt did not end with the interruption")
	}
	if len(deleted) != 0 {
		t.Error("deleted after an interruption")
	}
	events, _ := os.ReadFile(filepath.Join(state, "events.log"))
	if !strings.HasSuffix(string(events), " claude-web               down      interrupted\n") || strings.Contains(string(events), "FAILED") {
		t.Errorf("events %q", events)
	}
}

// syncBuffer is a buffer that a test may read while a command still writes to it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type fakeUp struct{}

func (fakeUp) Up(context.Context, *model.Machine, string, runlog.Log, io.Writer, io.Writer) (int, error) {
	return 0, nil
}

func TestNoCodeIsPassedToTheUseCase(t *testing.T) {
	env, _, o := preflightSetup(t, false)
	var got driving.UpOptions
	o.host().services.Up = func(_ *model.Machine, uo driving.UpOptions) driving.Up {
		got = uo
		return fakeUp{}
	}
	var out, errb bytes.Buffer
	if err := runUp(context.Background(), o, env, &out, &errb); err != nil || got.NoCode || got.Sleep == nil {
		t.Fatalf("by default: %v %+v", err, got)
	}
	_, po, err := parse([]string{"up", "web", "--no-code"})
	if err != nil || !po.noCode {
		t.Fatalf("--no-code: %v %+v", err, po)
	}
	o.noCode = po.noCode
	if err := runUp(context.Background(), o, env, &out, &errb); err != nil || !got.NoCode {
		t.Errorf("--no-code is not passed: %v %+v", err, got)
	}
	for _, args := range [][]string{{"check", "web", "--no-code"}, {"up", "web", "--no-code=1"}} {
		if _, _, err := parse(args); err == nil {
			t.Errorf("%v: want an error", args)
		}
	}
}
