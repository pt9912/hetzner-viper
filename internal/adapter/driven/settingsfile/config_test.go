package settingsfile_test

import (
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/settingsfile"
)

func envOf(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func source(t *testing.T, src string, env map[string]string) (map[string]string, error) {
	t.Helper()
	vars := map[string]string{}
	err := settingsfile.Source(vars, src, envOf(env))
	return vars, err
}

func TestSourceReadsWhatTheExampleFileContains(t *testing.T) {
	src := `SERVER_PREFIX=claude

#https://www.hetzner.com/
SERVER_TYPE=cpx32   # trailing comment
DNS_DOMAIN=
SSH_KEY="$HOME/.ssh/hetzner-build"
SSH_ALLOWED_CIDRS=0.0.0.0/0,::/0
# VIPER_HOMEBREW=1
export WORKSPACE_DIR=/Development
VIPER_BREW_PACKAGES="ripgrep fd
  hashicorp/tap/terraform"
`
	got, err := source(t, src, map[string]string{"HOME": "/home/me"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"SERVER_PREFIX": "claude", "SERVER_TYPE": "cpx32", "DNS_DOMAIN": "", "SSH_KEY": "/home/me/.ssh/hetzner-build",
		"SSH_ALLOWED_CIDRS": "0.0.0.0/0,::/0", "WORKSPACE_DIR": "/Development",
		"VIPER_BREW_PACKAGES": "ripgrep fd\n  hashicorp/tap/terraform",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
	// the template of the repository itself
	b, err := os.ReadFile("../../../../.env.local.example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source(t, string(b), map[string]string{"HOME": "/h"}); err != nil {
		t.Errorf(".env.local.example: %v", err)
	}
}

func TestSourceQuotingLikeBash(t *testing.T) {
	env := map[string]string{"HOME": "/h", "FROMENV": "e", "EMPTY": ""}
	cases := []struct{ src, want string }{
		{`A=plain`, "plain"},
		{`A='single $HOME "q"'`, `single $HOME "q"`},
		{`A="dq $HOME ${FROMENV} \$HOME \"q\" \\ x"`, `dq /h e $HOME "q" \ x`},
		{`A=x"y z"'w v'u`, "xy zw vu"},
		{`A=~/k`, "/h/k"},
		{`A=a~b`, "a~b"},
		{"A=$FROMENV$FROMENV", "ee"},
		{`A=$FROMENV-x`, "e-x"},
		{`A=EMPTY$EMPTY`, "EMPTY"},
		{`A=a:~/b`, "a:/h/b"},
		{`A=~;B=x`, "/h"},
		{`A=x\`, `x\`},
		{`A=cost$`, "cost$"},
		{`A=#notacomment`, "#notacomment"},
		{`A=a\ b`, "a b"},
		{"A=a\\\nb", "ab"},
		{`A="p@ss word!"`, "p@ss word!"},
		{`A=1; B=$A`, "1"},
	}
	for _, c := range cases {
		got, err := source(t, c.src, env)
		if err != nil || got["A"] != c.want {
			t.Errorf("%q: got %q, %v; want %q", c.src, got["A"], err, c.want)
		}
	}
	got, _ := source(t, "A=1\nB=\"$A-x\"\nA=2\nC=$A", nil)
	if got["B"] != "1-x" || got["C"] != "2" {
		t.Errorf("assignments see the earlier ones: %q", got)
	}
}

func TestSourceRefusesWhatItCannotRead(t *testing.T) {
	for _, src := range []string{
		"A=$(date)", "A=`date`", `A="$(date)"`, "A=${B:-x}", "A=$1", "A=$$", "A=x|y", "A=x&y", "echo hi",
		"A='open", `A="open`, "A=${B", "FOO BAR=1",
		"A=$UNSET", "A=$UNSET/k", `A="${UNSET}"`, "A=1\r\n", "A=$'a\\tb'", `A=$"x"`, "A=~root/x", "A=~+", "A=~-", "A=x:~user",
		"HCLOUD_TOKEN = secret", "export HCLOUD_TOKEN secret", "HCLOUD_TOKEN: secret", "A=${X:-secret}",
	} {
		if _, err := source(t, src, nil); err == nil {
			t.Errorf("%q: want an error", src)
		}
	}
	_, err := source(t, "A=1\nB=$(x)", nil)
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("line number: %v", err)
	}
	// the line number of an unterminated quote is where it starts; nothing of the line is printed
	for src, want := range map[string]string{"A=1\nB='x\ny\nz\n": "line 2", "A=1\nB=\"x\ny\nz\n": "line 2", "A=1\r\nB=2": "line 1"} {
		if _, err := source(t, src, nil); err == nil || !strings.HasPrefix(err.Error(), want+":") {
			t.Errorf("%q: got %v, want %s", src, err, want)
		}
	}
	for _, src := range []string{"HCLOUD_TOKEN = hunter2", "export HCLOUD_TOKEN hunter2", "HCLOUD_TOKEN: hunter2", "A=${X:-hunter2}", "A=\"hunter2$(x)\"", "A=hunter2|x"} {
		if _, err := source(t, src, nil); err == nil || strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%q: the error must not repeat the content: %v", src, err)
		}
	}
}

func setupDir(t *testing.T, env, machine string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	if machine != "" {
		if err := os.MkdirAll(filepath.Join(dir, "machines"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "machines", "web.env.local"), []byte(machine), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadDerivesNamesAndPaths(t *testing.T) {
	dir := setupDir(t, "DNS_DOMAIN=example.com\nSSH_KEY=~/.ssh/k\nSERVER_PREFIX=lab\n", "SERVER_TYPE=cpx52\n")
	env := settings.Env{Lookup: envOf(map[string]string{"HCLOUD_TOKEN": "h", "CLOUDFLARE_API_TOKEN": "c", "CLOUDFLARE_ZONE_ID": "z"}), Home: "/home/me"}
	c, err := settingsfile.Load(dir, "web", env, true)
	if err != nil {
		t.Fatal(err)
	}
	for got, want := range map[string]string{
		c.ServerName: "lab-web", c.SSHAlias: "hetzner-lab-web", c.DNSHost: "lab-web.example.com", c.KeyName: "lab-login",
		c.FirewallName: "lab-ssh", c.SSHKey: "/home/me/.ssh/k", c.ServerType: "cpx52", c.ServerLocation: "fsn1", c.ServerImage: "ubuntu-26.04",
		c.WorkspaceDir: "/Development", c.SSHConfig: "/home/me/.ssh/config_hetzner_lab-web",
		c.KnownPin: "/home/me/.ssh/known_hosts_hetzner_lab-web", c.StateDir: "/home/me/.local/state/hetzner-claude",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestLoadReportsEverySettingThatIsMissing(t *testing.T) {
	dir := setupDir(t, "DNS_DOMAIN=\n", "")
	env := settings.Env{Lookup: envOf(map[string]string{"CLOUDFLARE_ZONE_ID": "z"}), Home: "/h"}
	_, err := settingsfile.Load(dir, "web", env, true)
	if err == nil || !strings.HasPrefix(err.Error(), "Missing settings: DNS_DOMAIN HCLOUD_TOKEN CLOUDFLARE_API_TOKEN (in .env.local or machines/web.env.local;") {
		t.Errorf("got %v", err)
	}
	if _, err := settingsfile.Load(setupDir(t, "DNS_DOMAIN=example.com\n", ""), "web", env, false); err != nil {
		t.Errorf("tokens are not needed to reach a machine: %v", err)
	}
}

func TestLoadTakesHetznerAsTheDefaultProvider(t *testing.T) {
	env := settings.Env{Lookup: envOf(nil), Home: "/h"}
	c, err := settingsfile.Load(setupDir(t, "DNS_DOMAIN=example.com\n", ""), "web", env, false)
	if err != nil {
		t.Fatal(err)
	}
	if c.Provider != "hetzner" || c.ServerType != "cpx32" || c.ServerLocation != "fsn1" || c.ServerImage != "ubuntu-26.04" {
		t.Errorf("%s %s %s %s", c.Provider, c.ServerType, c.ServerLocation, c.ServerImage)
	}
}

func TestLoadIgnoresPROVIDERFromTheShellAndTakesItInLowerCase(t *testing.T) {
	env := settings.Env{Lookup: envOf(map[string]string{"PROVIDER": "aws"}), Home: "/h"}
	c, err := settingsfile.Load(setupDir(t, "DNS_DOMAIN=example.com\n", ""), "web", env, false)
	if err != nil || c.Provider != "hetzner" {
		t.Fatalf("the shell must not choose the provider: %v %v", c, err)
	}
	c, err = settingsfile.Load(setupDir(t, "DNS_DOMAIN=example.com\nPROVIDER=\" Hetzner \"\n", ""), "web", env, false)
	if err != nil || c.Provider != "hetzner" {
		t.Errorf("PROVIDER in the files: %v %v", c, err)
	}
}

func TestLoadRejectsAnUnknownProvider(t *testing.T) {
	env := settings.Env{Lookup: envOf(nil), Home: "/h"}
	_, err := settingsfile.Load(setupDir(t, "DNS_DOMAIN=example.com\nPROVIDER=nope\n", ""), "web", env, false)
	if err == nil || err.Error() != "PROVIDER: unknown provider nope (one of: aws, azure, gcp, hetzner)" {
		t.Errorf("got %v", err)
	}
}

func TestLoadFilesTakePrecedenceOverTheEnvironment(t *testing.T) {
	dir := setupDir(t, "DNS_DOMAIN=example.com\nHCLOUD_TOKEN=fromfile\n", "")
	env := settings.Env{Lookup: envOf(map[string]string{"HCLOUD_TOKEN": "fromshell", "SERVER_TYPE": "cpx11"}), Home: "/h"}
	c, err := settingsfile.Load(dir, "web", env, false)
	if err != nil {
		t.Fatal(err)
	}
	if c.Get("HCLOUD_TOKEN") != "fromfile" || c.ServerType != "cpx11" {
		t.Errorf("token %q, type %q", c.Get("HCLOUD_TOKEN"), c.ServerType)
	}
}

func TestLoadChecksNamesAndDomain(t *testing.T) {
	env := settings.Env{Lookup: envOf(nil), Home: "/h"}
	for _, c := range []struct{ file, machine, want string }{
		{"DNS_DOMAIN=example.com\n", "Web", "Machine name: lowercase letters, digits, hyphens"},
		{"DNS_DOMAIN=example.com\n", "-web", "Machine name: lowercase"},
		{"DNS_DOMAIN=https://example.com\n", "web", "DNS_DOMAIN must be a domain like example.com"},
		{"DNS_DOMAIN=Example.com\n", "web", "DNS_DOMAIN must be a domain"},
		{"DNS_DOMAIN=example.com\nSERVER_PREFIX=Bad\n", "web", "SERVER_PREFIX: lowercase letters, digits, hyphens: Bad"},
		{"DNS_DOMAIN=example.com\nSERVER_PREFIX=" + strings.Repeat("a", 60) + "\n", "web", "is longer than 63 characters"},
		{"DNS_DOMAIN=$(x)\n", "web", ".env.local"},
	} {
		if _, err := settingsfile.Load(setupDir(t, c.file, ""), c.machine, env, false); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q %q: got %v, want %q", c.file, c.machine, err, c.want)
		}
	}
	if _, err := settingsfile.Load(t.TempDir(), "web", env, false); err == nil || err.Error() != "No .env.local: run viper setup to create it" {
		t.Errorf("no file: %v", err)
	}
}

// The parser must read what the scripts read: the same variables with the same values as
// `set -euo pipefail; set -a; source file` in bash, and an error wherever bash fails.
func TestSourceAgreesWithBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	example, err := os.ReadFile("../../../../.env.local.example")
	if err != nil {
		t.Fatal(err)
	}
	baseEnv := map[string]string{"HOME": "/h", "FROMENV": "e", "EMPTY": ""}
	inBash := func(src string) (map[string]string, error) {
		f := filepath.Join(t.TempDir(), "f")
		if err := os.WriteFile(f, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("env", "-i", "HOME=/h", "FROMENV=e", "EMPTY=", bash, "-c",
			`set -euo pipefail; set -a; source "$1"; env -0`, "_", f).Output()
		if err != nil {
			return nil, err
		}
		got := map[string]string{}
		for _, kv := range strings.Split(string(out), "\x00") {
			k, v, ok := strings.Cut(kv, "=")
			if _, inBase := baseEnv[k]; !ok || k == "_" || k == "PWD" || k == "SHLVL" || (inBase && baseEnv[k] == v) {
				continue
			}
			got[k] = v
		}
		return got, nil
	}
	accepted := []string{
		`A=plain`, `A='single $HOME "q"'`, `A="dq $HOME ${FROMENV} \$HOME \"q\" \\ x"`, `A=x"y z"'w v'u`, `A=~/k`, `A=a~b`,
		"A=$FROMENV$FROMENV", `A=$EMPTY-x`, `A=cost$`, `A=#notacomment`, `A=a\ b`, "A=a\\\nb", `A="p@ss word!"`,
		"A=1; B=$A", "A=1\nB=\"$A-x\"\nA=2\nC=$A", "A=\"two\nlines\"", `A="back\slash \n"`, "A=1 B=2", "export A=3 # c\nexport B",
		"A=1 B=$A", "A=x #c", "A=#", "A=", "A=\"it's\"", "A=ü€", "export A B=2", `A=a:~/b`, "A=~;B=x", "A=~", `A=$`, `A="$"`, "A=1\n\n\n",
		`A=x\`, "A=$FROMENV/k", `A="${FROMENV}"`, string(example),
	}
	for _, src := range accepted {
		want, err := inBash(src)
		if err != nil {
			t.Errorf("%q: bash rejects it: %v", src, err)
			continue
		}
		got := map[string]string{}
		if err := settingsfile.Source(got, src, envOf(baseEnv)); err != nil {
			t.Errorf("%q: %v", src, err)
			continue
		}
		for k, v := range got { // names that bash exports because of set -a only
			if bv, ok := want[k]; !ok || bv != v {
				t.Errorf("%q: %s = %q in Go, %q (set: %v) in bash", src, k, v, bv, ok)
			}
		}
		for k, v := range want {
			if gv, ok := got[k]; !ok || gv != v {
				t.Errorf("%q: %s = %q in bash, %q (set: %v) in Go", src, k, v, gv, ok)
			}
		}
	}
	// bash fails (set -u, syntax): so must the reader
	for _, src := range []string{"A=$UNSET", "A=$UNSET/k", `A="${UNSET}"`, "A='open", `A="open`, "A=${B", "A=$(false)", "echo hi"} {
		if _, err := inBash(src); err == nil && !strings.HasPrefix(src, "echo") {
			t.Errorf("%q: expected bash to fail", src)
		}
		if err := settingsfile.Source(map[string]string{}, src, envOf(baseEnv)); err == nil {
			t.Errorf("%q: bash fails, the reader accepted it", src)
		}
	}
}

func TestPrefixIsReadLikeTheScriptsReadIt(t *testing.T) {
	dir := setupDir(t, "SERVER_PREFIX=cs      # not claude\n", "SERVER_TYPE=cpx32\n")
	env := settings.Env{Home: "/h"}
	if got := settingsfile.Prefix(dir, "web", env); got != "cs" {
		t.Errorf("comment after the value: %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "machines", "web.env.local"), []byte("SERVER_PREFIX=\"mach\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := settingsfile.Prefix(dir, "web", env); got != "mach" {
		t.Errorf("machine file wins, quotes removed: %q", got)
	}
	if got := settingsfile.Prefix(t.TempDir(), "web", env); got != "claude" {
		t.Errorf("default: %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "machines", "web.env.local"), []byte("SERVER_PREFIX=$(x)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := settingsfile.Prefix(dir, "web", env); got != "cs" {
		t.Errorf("an unreadable machine file is ignored: %q", got)
	}
}

func TestEnvWithoutLookupIsSafe(t *testing.T) {
	dir := setupDir(t, "DNS_DOMAIN=example.com\n", "")
	c, err := settingsfile.Load(dir, "web", settings.Env{Home: "/h"}, false)
	if err != nil || c.SSHKey != "/h/.ssh/hetzner-build" {
		t.Errorf("%v %v", c, err)
	}
}

func TestKeyPath(t *testing.T) {
	for _, c := range []struct{ setting, want string }{
		{"", "/h/.ssh/hetzner-build"}, {"~/.ssh/k", "/h/.ssh/k"}, {"/abs/k", "/abs/k"}, {"keys/k", "/work/keys/k"}, {"./k", "/work/k"},
	} {
		if got := model.KeyPath(c.setting, "/work", "/h"); got != c.want {
			t.Errorf("%q: got %q, want %q", c.setting, got, c.want)
		}
	}
}
