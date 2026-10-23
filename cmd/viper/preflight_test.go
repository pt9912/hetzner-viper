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
	"testing"

	"github.com/pt9912/hetzner-viper/internal/check"
	"github.com/pt9912/hetzner-viper/internal/config"
	"github.com/pt9912/hetzner-viper/internal/hetzner"
	"github.com/pt9912/hetzner-viper/internal/preflight"
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
	if commands["up"].native != nil || commands["down"].native != nil {
		t.Error("up and down are still the scripts")
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
