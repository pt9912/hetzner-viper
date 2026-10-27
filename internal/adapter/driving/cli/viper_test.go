package cli_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/cli"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

func TestAMissingMachineNameGivesTheUsage(t *testing.T) {
	for _, name := range []string{"preflight", "up", "check", "ssh", "vscode", "down", "dns"} {
		_, _, err := cli.Parse([]string{name})
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
		if _, _, err := cli.Parse(args); err == nil {
			t.Errorf("%v: want an error", args)
		}
	}
	if _, _, err := cli.Parse([]string{"up", "-h"}); !cli.IsHelp(err) {
		t.Errorf("-h: %v", err)
	}
}

func nativeEnv(t *testing.T) (settings.Env, string) {
	t.Helper()
	home := t.TempDir()
	return settings.Env{Lookup: func(k string) (string, bool) { v, ok := map[string]string{"HOME": home}[k]; return v, ok }, Home: home}, home
}

// fakeTools records the programs run on the host; ssh-keygen creates the key files it would.
func fakeTools(t *testing.T) (*[][]string, *cli.Host) {
	t.Helper()
	var calls [][]string
	h := testHost()
	h.LookPath = func(string) (string, error) { return "/usr/bin/tool", nil }
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
			if err := os.WriteFile(f, []byte("PRIVATE\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return os.WriteFile(f+".pub", []byte("ssh-ed25519 AAAAkey comment\n"), 0o644)
		}
		return nil
	}
	h.RunTool, h.ExecTool = record, record
	return &calls, h
}

func TestSetupCreatesTheKeysAndTrustsTheCA(t *testing.T) {
	env, home := nativeEnv(t)
	dir := t.TempDir()
	calls, h := fakeTools(t)
	o := &cli.Opts{Host: h, Dir: dir}
	var out strings.Builder
	// no .env.local yet: created from the template (it has no DNS_DOMAIN)
	if err := cli.Setup(o, env, &out); err == nil || !strings.Contains(err.Error(), "Set DNS_DOMAIN in .env.local") {
		t.Fatalf("got %v", err)
	}
	st, err := os.Stat(filepath.Join(dir, ".env.local"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("template copied with mode 600: %v %v", st, err)
	}
	if len(*calls) != 0 {
		t.Errorf("nothing generated without a domain: %q", *calls)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cli.Setup(o, env, &out); err != nil {
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
	if err := cli.Setup(o, env, &out); err != nil || len(*calls) != 0 {
		t.Errorf("existing keys are kept: %v %q", err, *calls)
	}
}

func TestSetupUsesTheConfiguredKeyPath(t *testing.T) {
	env, home := nativeEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\nSSH_KEY=~/keys/mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls, h := fakeTools(t)
	if err := cli.Setup(&cli.Opts{Host: h, Dir: dir}, env, io.Discard); err != nil {
		t.Fatal(err)
	}
	if (*calls)[0][4] != filepath.Join(home, "keys", "mine") {
		t.Errorf("calls %q", *calls)
	}
}

func TestConnectUsesTheMachinesOwnSshConfig(t *testing.T) {
	env, home := nativeEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls, h := fakeTools(t)
	o := &cli.Opts{Host: h, Dir: dir, Machine: "web"}
	if err := cli.Connect(o, env, true); err == nil || err.Error() != "No SSH config for claude-web: run viper up web first" {
		t.Fatalf("got %v", err)
	}
	cfg := filepath.Join(home, ".ssh", "config_hetzner_claude-web")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cli.Connect(o, env, true); err != nil {
		t.Fatal(err)
	}
	o.Cmd = "tmux attach; echo \"$HOME\""
	if err := cli.Connect(o, env, true); err != nil {
		t.Fatal(err)
	}
	if err := cli.Connect(o, env, false); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"ssh", "-F", cfg, "hetzner-claude-web"},
		{"ssh", "-t", "-F", cfg, "hetzner-claude-web", o.Cmd},
		{"ssh", "-F", cfg, "hetzner-claude-web", o.Cmd},
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Errorf("calls %q", *calls)
	}
}

func TestDnsNamePrintsTheMachinesName(t *testing.T) {
	env, _ := nativeEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\nSERVER_PREFIX=lab\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := cli.DNSName(&cli.Opts{Host: testHost(), Dir: dir, Machine: "web"}, env, &out); err != nil || out.String() != "lab-web.example.com\n" {
		t.Errorf("%q %v", out.String(), err)
	}
	if err := cli.DNSName(&cli.Opts{Host: testHost(), Dir: t.TempDir(), Machine: "web"}, env, &out); err == nil {
		t.Error("no settings")
	}
}

func TestHelpPointsToTheManual(t *testing.T) {
	rel := cli.ManualLines("v0.5.1")
	for _, want := range []string{"/blob/v0.5.1/docs/user/handbuch.md", "/blob/main/docs/user/handbuch.md"} {
		if !strings.Contains(rel, want) {
			t.Errorf("release: %q lacks %s", rel, want)
		}
	}
	for _, v := range []string{"dev", "v0.5.1-3-gabc123-dirty", ""} {
		got := cli.ManualLines(v)
		if !strings.Contains(got, "/blob/main/") || strings.Count(got, "http") != 1 {
			t.Errorf("%q: %s", v, got)
		}
	}
	if !strings.Contains(cli.HelpText("dev"), "Manual (German)") {
		t.Errorf("help: %s", cli.HelpText("dev"))
	}
}

func TestConnectWithTmuxAttachesToTheClaudeSession(t *testing.T) {
	env, home := nativeEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(home, ".ssh", "config_hetzner_claude-web")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	calls, h := fakeTools(t)
	o := &cli.Opts{Host: h, Dir: dir, Machine: "web", Tmux: true}
	if err := cli.Connect(o, env, true); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"ssh", "-t", "-F", cfg, "hetzner-claude-web", cli.TmuxCommand}}
	if !reflect.DeepEqual(*calls, want) || !strings.Contains(cli.TmuxCommand, "new-session -A -s claude") {
		t.Errorf("calls %q", *calls)
	}
	if err := cli.Connect(o, env, false); err == nil || !strings.Contains(err.Error(), "needs a terminal") {
		t.Errorf("without a terminal: %v", err)
	}
	o.Cmd = "ls"
	if err := cli.Connect(o, env, true); err == nil || !strings.Contains(err.Error(), "exclude each other") {
		t.Errorf("with --cmd: %v", err)
	}
	if len(*calls) != 1 {
		t.Errorf("ssh ran for a refused call: %q", *calls)
	}
}

func TestDnsNameWithoutDNSGivesTheAddress(t *testing.T) {
	env, home := nativeEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_PROVIDER=none\nSERVER_PREFIX=lab\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	opts := &cli.Opts{Host: testHost(), Dir: dir, Machine: "web"}
	if err := cli.DNSName(opts, env, &out); err != nil || !strings.Contains(out.String(), "no address is recorded for lab-web") {
		t.Errorf("not running: %q %v", out.String(), err)
	}
	state := filepath.Join(home, ".local", "state", "hetzner-claude")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "ip-lab-web"), []byte("203.0.113.7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := cli.DNSName(opts, env, &out); err != nil || out.String() != "No DNS name (DNS_PROVIDER=none); the address of lab-web is 203.0.113.7\n" {
		t.Errorf("running: %q %v", out.String(), err)
	}
}
