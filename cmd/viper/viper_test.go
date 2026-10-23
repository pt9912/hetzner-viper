package main

import (
	"errors"
	"github.com/pt9912/hetzner-viper/internal/config"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func mustParse(t *testing.T, args ...string) (string, options) {
	t.Helper()
	name, o, err := parse(args)
	if err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return name, o
}

func TestAMissingMachineNameGivesTheUsage(t *testing.T) {
	for _, name := range []string{"preflight", "up", "check", "ssh", "down", "dns"} {
		_, _, err := parse([]string{name})
		if err == nil || !strings.HasPrefix(err.Error(), "Usage: viper "+name+" <machine>") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestUnknownOrMisplacedOptionsAreRejected(t *testing.T) {
	for _, args := range [][]string{
		{"nope"}, {"up", "web", "--bogus"}, {"check", "web", "--repo", "x"}, {"up", "web", "--force"},
		{"up", "web", "--repo"}, {"down", "web", "--force=1"}, {"setup", "web"}, {"up", "a", "b"},
	} {
		if _, _, err := parse(args); err == nil {
			t.Errorf("%v: want an error", args)
		}
	}
	if _, _, err := parse([]string{"up", "-h"}); !errors.Is(err, errHelp) {
		t.Errorf("-h: %v", err)
	}
}

func nativeEnv(t *testing.T) (config.Env, string) {
	t.Helper()
	home := t.TempDir()
	return config.Env{Lookup: func(k string) (string, bool) { v, ok := map[string]string{"HOME": home}[k]; return v, ok }, Home: home}, home
}

// fakeTools records the programs run on the host; ssh-keygen creates the key files it would.
func fakeTools(t *testing.T) *[][]string {
	t.Helper()
	var calls [][]string
	oldRun, oldExec, oldLook := runTool, execTool, lookPath
	t.Cleanup(func() { runTool, execTool, lookPath = oldRun, oldExec, oldLook })
	lookPath = func(string) (string, error) { return "/usr/bin/tool", nil }
	record := func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		if name == "ssh-keygen" {
			f := args[slices.Index(args, "-f")+1]
			if _, err := os.Stat(f); err == nil {
				return errors.New("ssh-keygen would overwrite " + f)
			}
			if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
				return err
			}
			os.WriteFile(f, []byte("PRIVATE\n"), 0o600)
			return os.WriteFile(f+".pub", []byte("ssh-ed25519 AAAAkey comment\n"), 0o644)
		}
		return nil
	}
	runTool, execTool = record, record
	return &calls
}

func TestSetupCreatesTheKeysAndTrustsTheCA(t *testing.T) {
	env, home := nativeEnv(t)
	dir := t.TempDir()
	calls := fakeTools(t)
	o := &options{dir: dir}
	var out strings.Builder
	// no .env.local yet: created from the template (it has no DNS_DOMAIN)
	if err := setup(o, env, &out); err == nil || !strings.Contains(err.Error(), "Set DNS_DOMAIN in .env.local") {
		t.Fatalf("got %v", err)
	}
	st, err := os.Stat(filepath.Join(dir, ".env.local"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("template copied with mode 600: %v %v", st, err)
	}
	if len(*calls) != 0 {
		t.Errorf("nothing generated without a domain: %q", *calls)
	}
	os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\n"), 0o600)
	if err := setup(o, env, &out); err != nil {
		t.Fatal(err)
	}
	key, ca := filepath.Join(home, ".ssh", "hetzner-build"), filepath.Join(home, ".ssh", "hetzner_claude_host_ca")
	want := [][]string{
		{"ssh-keygen", "-t", "ed25519", "-f", key, "-N", ""},
		{"ssh-keygen", "-t", "ed25519", "-f", ca, "-N", "", "-C", "Hetzner Claude Host CA"},
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Errorf("calls %q", *calls)
	}
	b, _ := os.ReadFile(filepath.Join(home, ".ssh", "known_hosts_hetzner_claude_ca"))
	if string(b) != "@cert-authority *.example.com ssh-ed25519 AAAAkey comment\n" {
		t.Errorf("known hosts %q", b)
	}
	if !strings.Contains(out.String(), "Setup complete.") || !strings.Contains(out.String(), "ssh-ed25519 AAAAkey comment\n") {
		t.Errorf("output %q", out.String())
	}
	// a second run keeps the keys
	*calls = nil
	if err := setup(o, env, &out); err != nil || len(*calls) != 0 {
		t.Errorf("existing keys are kept: %v %q", err, *calls)
	}
}

func TestSetupUsesTheConfiguredKeyPath(t *testing.T) {
	env, home := nativeEnv(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\nSSH_KEY=~/keys/mine\n"), 0o600)
	calls := fakeTools(t)
	if err := setup(&options{dir: dir}, env, io.Discard); err != nil {
		t.Fatal(err)
	}
	if (*calls)[0][4] != filepath.Join(home, "keys", "mine") {
		t.Errorf("calls %q", *calls)
	}
}

func TestConnectUsesTheMachinesOwnSshConfig(t *testing.T) {
	env, home := nativeEnv(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\n"), 0o600)
	calls := fakeTools(t)
	o := &options{dir: dir, machine: "web"}
	if err := connect(o, env, true); err == nil || err.Error() != "No SSH config for claude-web: run viper up web first" {
		t.Fatalf("got %v", err)
	}
	cfg := filepath.Join(home, ".ssh", "config_hetzner_claude-web")
	os.MkdirAll(filepath.Dir(cfg), 0o700)
	os.WriteFile(cfg, nil, 0o600)
	if err := connect(o, env, true); err != nil {
		t.Fatal(err)
	}
	o.cmd = "tmux attach; echo \"$HOME\""
	if err := connect(o, env, true); err != nil {
		t.Fatal(err)
	}
	if err := connect(o, env, false); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"ssh", "-F", cfg, "hetzner-claude-web"},
		{"ssh", "-t", "-F", cfg, "hetzner-claude-web", o.cmd},
		{"ssh", "-F", cfg, "hetzner-claude-web", o.cmd},
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Errorf("calls %q", *calls)
	}
}

func TestDnsNamePrintsTheMachinesName(t *testing.T) {
	env, _ := nativeEnv(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\nSERVER_PREFIX=lab\n"), 0o600)
	var out strings.Builder
	if err := dnsName(&options{dir: dir, machine: "web"}, env, &out); err != nil || out.String() != "lab-web.example.com\n" {
		t.Errorf("%q %v", out.String(), err)
	}
	if err := dnsName(&options{dir: t.TempDir(), machine: "web"}, env, &out); err == nil {
		t.Error("no settings")
	}
}
