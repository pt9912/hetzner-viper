package main

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

	"github.com/pt9912/hetzner-viper/internal/check"
	"github.com/pt9912/hetzner-viper/internal/config"
	"github.com/pt9912/hetzner-viper/internal/hetzner"
	"github.com/pt9912/hetzner-viper/internal/preflight"
	"github.com/pt9912/hetzner-viper/internal/start"
	"github.com/pt9912/hetzner-viper/internal/stop"
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
	os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\n"), 0o600)
	ssh := filepath.Join(home, ".ssh")
	os.MkdirAll(ssh, 0o700)
	for _, f := range []string{"hetzner-build", "hetzner-build.pub", "hetzner_claude_host_ca"} {
		os.WriteFile(filepath.Join(ssh, f), []byte("ssh-ed25519 AAAA k\n"), 0o600)
	}
	os.WriteFile(filepath.Join(ssh, "known_hosts_hetzner_claude_ca"), []byte("@cert-authority *.example.com AAAA\n"), 0o600)
	old := newPreflightDeps
	t.Cleanup(func() { newPreflightDeps = old })
	newPreflightDeps = func(*config.Config) preflight.Deps {
		return preflight.Deps{Hetzner: okHetzner{}, DNS: okDNS{ok: dnsOK}}
	}
	return env, home, &options{dir: dir, machine: "web"}
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
	if commands["preflight"].native == nil {
		t.Error("preflight is native")
	}
	for _, name := range []string{"up", "down", "check", "log", "setup", "ssh", "dns"} {
		if commands[name].native == nil {
			t.Errorf("%s is native", name)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type serverHetzner struct{ srv *hetzner.Server }

func (s serverHetzner) ServerByName(context.Context, string) (*hetzner.Server, error) {
	return s.srv, nil
}

func TestCheckLogsTheRunAndItsOutcome(t *testing.T) {
	env, home, o := preflightSetup(t, true)
	state := filepath.Join(home, ".local", "state", "hetzner-claude")
	old := newCheckDeps
	t.Cleanup(func() { newCheckDeps = old })
	var probeOK = true
	newCheckDeps = func(c *config.Config) check.Deps {
		return check.Deps{
			Hetzner: serverHetzner{&hetzner.Server{Name: "claude-web", IPv4: "203.0.113.7"}},
			HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"Answer":[{"type":1,"data":"203.0.113.7"}]}`))}, nil
			})},
			LookupIPv4:     func(context.Context, string) ([]string, error) { return []string{"203.0.113.7"}, nil },
			WriteSSHConfig: func(string) error { return nil },
			Run: func(_ context.Context, _, name string, args ...string) (string, string, error) {
				if name == "ssh" && args[0] != "-n" && !probeOK {
					return "", "", errors.New("exit 255")
				}
				if name == "ssh" && args[0] != "-n" {
					return "cloudinit=done\nclaude_version=2\ngh_version=gh 2\ngit_name=a\ngit_email=b@c\ndocker=ok\nbrew_version=Homebrew 5\nbrew_path=yes\nsshd=authenticationmethods publickey;passwordauthentication no;permitrootlogin no;\n", "", nil
				}
				return "", "", nil
			},
		}
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
	if commands["check"].native == nil {
		t.Error("check is native")
	}
}

type downHetzner struct {
	srv     *hetzner.Server
	deleted *[]int64
}

func (d downHetzner) ServerByName(context.Context, string) (*hetzner.Server, error) {
	return d.srv, nil
}
func (d downHetzner) DeleteServer(_ context.Context, id int64) error {
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
	var deleted []int64
	old := newDownDeps
	t.Cleanup(func() { newDownDeps = old })
	newDownDeps = func(c *config.Config) stop.Deps {
		return stop.Deps{
			Hetzner: downHetzner{&hetzner.Server{ID: 42, Name: "claude-web", IPv4: "203.0.113.7"}, &deleted},
			DNS:     dnsDeleter{},
			Run: func(_ context.Context, _, name string, args ...string) (string, string, error) {
				if strings.HasPrefix(args[len(args)-1], "bash -s") {
					return "== /Development ==\n== tmux sessions ==\nUNSAVED WORK: x\n== end of check ==\n", "", nil
				}
				return "ssh-ed25519 AAAA k\n", "", nil
			},
			WriteSSHConfig: func(string) error { return nil },
			SSHInclude:     func(string) error { return nil },
		}
	}
	// both answers follow each other in one pipe
	var out, errb bytes.Buffer
	err := runDown(context.Background(), o, env, strings.NewReader("DISCARD\nDELETE\n"), &out, &errb)
	if err != nil || len(deleted) != 1 || deleted[0] != 42 {
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
	if commands["down"].native == nil {
		t.Error("down is native")
	}
}

func TestVSCodeDoesNotInheritTheTokens(t *testing.T) {
	env := vscodeEnv([]string{"PATH=/bin", "HCLOUD_TOKEN=a", "CLOUDFLARE_API_TOKEN=b", "CLOUDFLARE_ZONE_ID=z", "VIPER_GH_TOKEN=c", "GH_TOKEN=d",
		"VIPER_CLAUDE_TOKEN=e", "CLAUDE_CODE_OAUTH_TOKEN=f", "VIPER_ANTHROPIC_AUTH_TOKEN=g", "VIPER_CODE_SERVER_PASSWORD=h", "VIPER_TRANSCRIPT=x",
		"HOME=/home/me", "VIPER_SWAP=1", "TOKEN_LIKE=keep"})
	if strings.Join(env, " ") != "PATH=/bin HOME=/home/me VIPER_SWAP=1 TOKEN_LIKE=keep" {
		t.Errorf("%q", env)
	}
}

func TestUpStopsAtPreflightProblemsAndLogsThem(t *testing.T) {
	env, home, o := preflightSetup(t, false) // the DNS check fails: one problem
	old := newUpDeps
	t.Cleanup(func() { newUpDeps = old })
	created := false
	newUpDeps = func(c *config.Config, pd func() preflight.Deps) start.Deps {
		d := old(c, pd)
		d.Hetzner = failIfCreated{&created}
		return d
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

type failIfCreated struct{ created *bool }

func (f failIfCreated) ServerByName(context.Context, string) (*hetzner.Server, error) {
	return nil, nil
}
func (f failIfCreated) SSHKeys(context.Context) ([]hetzner.SSHKey, error) { return nil, nil }
func (f failIfCreated) SSHKeyCreate(context.Context, string, string) error {
	*f.created = true
	return nil
}
func (f failIfCreated) FirewallRules(context.Context, string) ([]hetzner.FirewallRule, bool, error) {
	return nil, false, nil
}
func (f failIfCreated) FirewallCreate(context.Context, string) error { *f.created = true; return nil }
func (f failIfCreated) FirewallAddRule(context.Context, string, hetzner.FirewallRule) error {
	*f.created = true
	return nil
}
func (f failIfCreated) ServerCreate(context.Context, hetzner.ServerSpec) (*hetzner.Server, error) {
	*f.created = true
	return nil, nil
}

func TestInterruptionAtAPromptIsLoggedNotAFailure(t *testing.T) {
	env, home, o := preflightSetup(t, true)
	state := filepath.Join(home, ".local", "state", "hetzner-claude")
	var deleted []int64
	old := newDownDeps
	t.Cleanup(func() { newDownDeps = old })
	newDownDeps = func(c *config.Config) stop.Deps {
		return stop.Deps{
			Hetzner: downHetzner{&hetzner.Server{ID: 42, Name: "claude-web", IPv4: "203.0.113.7"}, &deleted},
			DNS:     dnsDeleter{},
			Run: func(_ context.Context, _, _ string, args ...string) (string, string, error) {
				if strings.HasPrefix(args[len(args)-1], "bash -s") {
					return "== /Development ==\n== tmux sessions ==\n== end of check ==\n", "", nil
				}
				return "ssh-ed25519 AAAA k\n", "", nil
			},
			WriteSSHConfig: func(string) error { return nil },
			SSHInclude:     func(string) error { return nil },
		}
	}
	pr, pw := io.Pipe() // nothing is typed: the prompt waits
	defer pw.Close()
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
