package settingsfile_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/settingsfile"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
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
export WORKSPACE_DIR=/work
VIPER_BREW_PACKAGES="ripgrep fd
  hashicorp/tap/terraform"
`
	got, err := source(t, src, map[string]string{"HOME": "/home/me"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"SERVER_PREFIX": "claude", "SERVER_TYPE": "cpx32", "DNS_DOMAIN": "", "SSH_KEY": "/home/me/.ssh/hetzner-build",
		"SSH_ALLOWED_CIDRS": "0.0.0.0/0,::/0", "WORKSPACE_DIR": "/work",
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
	if runtime.GOOS == "windows" {
		t.Skip("expects Unix paths")
	}
	dir := setupDir(t, "DNS_DOMAIN=example.com\nSSH_KEY=~/.ssh/k\nSERVER_PREFIX=lab\n", "SERVER_TYPE=cpx52\n")
	env := settings.Env{Lookup: envOf(map[string]string{"HCLOUD_TOKEN": "h", "CLOUDFLARE_API_TOKEN": "c", "CLOUDFLARE_ZONE_ID": "z"}), Home: "/home/me"}
	c, err := settingsfile.Load(dir, "web", env, true)
	if err != nil {
		t.Fatal(err)
	}
	for got, want := range map[string]string{
		c.ServerName: "lab-web", c.SSHAlias: "hetzner-lab-web", c.DNSHost: "lab-web.example.com", c.KeyName: "lab-login",
		c.FirewallName: "lab-ssh", c.SSHKey: "/home/me/.ssh/k", c.ServerType: "cpx52", c.ServerLocation: "fsn1", c.ServerImage: "ubuntu-26.04",
		c.WorkspaceDir: settingsfile.DefaultWorkspace, c.SSHConfig: "/home/me/.ssh/config_hetzner_lab-web",
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

func TestLoadRejectsBadAWSTagsOnlyForAWS(t *testing.T) {
	env := settings.Env{Lookup: envOf(nil), Home: "/h"}
	_, err := settingsfile.Load(setupDir(t, "DNS_DOMAIN=example.com\nPROVIDER=aws\nAWS_TAGS=owner\n", ""), "web", env, false)
	if err == nil || !strings.Contains(err.Error(), "AWS_TAGS") {
		t.Errorf("a bad AWS_TAGS must stop the load: %v", err)
	}
	if _, err := settingsfile.Load(setupDir(t, "DNS_DOMAIN=example.com\nPROVIDER=aws\nAWS_TAGS=owner=me,ttl=2026-11-01T12:00:00Z\n", ""), "web", env, false); err != nil {
		t.Errorf("good tags: %v", err)
	}
	if _, err := settingsfile.Load(setupDir(t, "DNS_DOMAIN=example.com\nPROVIDER=aws\nAWS_TAGS_SHARED=project=x,Name=y\n", ""), "web", env, false); err == nil || !strings.Contains(err.Error(), "AWS_TAGS_SHARED") {
		t.Errorf("a bad AWS_TAGS_SHARED must stop the load: %v", err)
	}
	if _, err := settingsfile.Load(setupDir(t, "DNS_DOMAIN=example.com\nAWS_TAGS=owner\n", ""), "web", env, false); err != nil {
		t.Errorf("AWS_TAGS means nothing for Hetzner: %v", err)
	}
}

func TestLoadRejectsADiskSizeThatIsNoNumber(t *testing.T) {
	env := settings.Env{Lookup: envOf(nil), Home: "/h"}
	for _, bad := range []string{"100GB", "0", "-5", "99999", "abc"} {
		_, err := settingsfile.Load(setupDir(t, "DNS_DOMAIN=example.com\nPROVIDER=aws\nAWS_ROOT_GB="+bad+"\n", ""), "web", env, false)
		if err == nil || !strings.Contains(err.Error(), "AWS_ROOT_GB must be a whole number of GB") {
			t.Errorf("%q: got %v", bad, err)
		}
	}
	if _, err := settingsfile.Load(setupDir(t, "DNS_DOMAIN=example.com\nPROVIDER=aws\nAWS_ROOT_GB=100\n", ""), "web", env, false); err != nil {
		t.Errorf("100 is fine: %v", err)
	}
}

func TestLoadWithoutDNSNeedsNeitherADomainNorCloudflare(t *testing.T) {
	env := settings.Env{Lookup: envOf(map[string]string{"HCLOUD_TOKEN": "h"}), Home: "/home/me"}
	c, err := settingsfile.Load(setupDir(t, "DNS_PROVIDER=none\nSERVER_PREFIX=lab\n", ""), "web", env, true)
	if err != nil {
		t.Fatal(err)
	}
	if c.DNSProvider != "none" || c.HasDNS() || c.DNSDomain != "viper.invalid" || c.DNSHost != "lab-web.viper.invalid" {
		t.Errorf("%q %v %q %q", c.DNSProvider, c.HasDNS(), c.DNSDomain, c.DNSHost)
	}
	// the cloud's own credentials are still required
	if _, err := settingsfile.Load(setupDir(t, "DNS_PROVIDER=none\n", ""), "web", settings.Env{Lookup: envOf(nil), Home: "/h"}, true); err == nil || !strings.HasPrefix(err.Error(), "Missing settings: HCLOUD_TOKEN ") {
		t.Errorf("got %v", err)
	}
}

func TestLoadTakesCloudflareAsTheDefaultDNSAndChecksTheValue(t *testing.T) {
	env := settings.Env{Lookup: envOf(map[string]string{"DNS_PROVIDER": "none"}), Home: "/h"}
	c, err := settingsfile.Load(setupDir(t, "DNS_DOMAIN=example.com\n", ""), "web", env, false)
	if err != nil || c.DNSProvider != "cloudflare" || !c.HasDNS() {
		t.Errorf("the shell must not choose the DNS provider: %v %v", c, err)
	}
	_, err = settingsfile.Load(setupDir(t, "DNS_DOMAIN=example.com\nDNS_PROVIDER=route53\n", ""), "web", env, false)
	if err == nil || err.Error() != "DNS_PROVIDER: cloudflare or none: route53" {
		t.Errorf("got %v", err)
	}
}

func TestLoadRejectsAnUnknownProvider(t *testing.T) {
	env := settings.Env{Lookup: envOf(nil), Home: "/h"}
	_, err := settingsfile.Load(setupDir(t, "DNS_DOMAIN=example.com\nPROVIDER=nope\n", ""), "web", env, false)
	if err == nil || err.Error() != "PROVIDER: unknown provider nope (one of: aws, azure, compose, gcp, hetzner, kubernetes)" {
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
	if runtime.GOOS == "windows" {
		t.Skip("bash brings its own environment variables there")
	}
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
	if runtime.GOOS == "windows" {
		t.Skip("expects Unix paths")
	}
	dir := setupDir(t, "DNS_DOMAIN=example.com\n", "")
	c, err := settingsfile.Load(dir, "web", settings.Env{Home: "/h"}, false)
	if err != nil || c.SSHKey != "/h/.ssh/hetzner-build" {
		t.Errorf("%v %v", c, err)
	}
}

func TestKeyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("expects Unix paths")
	}
	for _, c := range []struct{ setting, want string }{
		{"", "/h/.ssh/hetzner-build"}, {"~/.ssh/k", "/h/.ssh/k"}, {"/abs/k", "/abs/k"}, {"keys/k", "/work/keys/k"}, {"./k", "/work/k"},
	} {
		if got := model.KeyPath(c.setting, "/work", "/h"); got != c.want {
			t.Errorf("%q: got %q, want %q", c.setting, got, c.want)
		}
	}
}

func TestLoadReadsNATSOnlyWhenEnabled(t *testing.T) {
	env := settings.Env{Lookup: envOf(nil), Home: "/h"}
	c, err := settingsfile.Load(setupDir(t, "DNS_DOMAIN=example.com\n", ""), "web", env, false)
	if err != nil || !reflect.DeepEqual(c.NATS, model.NATSConfig{}) {
		t.Fatalf("NATS is off by default: %+v %v", c.NATS, err)
	}
	// with NATS off, a bad group is not looked at: the settings read as before
	c, err = settingsfile.Load(setupDir(t, "DNS_DOMAIN=example.com\nNATS_GROUP=Bad.Name\nNATS_PORT=x\n", ""), "web", env, false)
	if err != nil || c.NATS.Enabled {
		t.Fatalf("NATS off: %+v %v", c.NATS, err)
	}
	c, err = settingsfile.Load(setupDir(t, "DNS_DOMAIN=example.com\nNATS_ENABLED=true\nNATS_GROUP=entwicklung\n", ""), "web", env, false)
	if err != nil || !reflect.DeepEqual(c.NATS, model.NATSConfig{Enabled: true, Group: "entwicklung", Port: 4222, Operator: "operator", MCPAutoAllow: "off"}) {
		t.Fatalf("NATS on: %+v %v", c.NATS, err)
	}
	c, err = settingsfile.Load(setupDir(t, "DNS_DOMAIN=example.com\nNATS_ENABLED=1\nNATS_GROUP=g\n", "NATS_PORT=4333\n"), "web", env, false)
	if err != nil || c.NATS.Port != 4333 {
		t.Fatalf("the machine's file wins: %+v %v", c.NATS, err)
	}
}

func TestLoadRefusesBadNATSSettings(t *testing.T) {
	env := settings.Env{Lookup: envOf(nil), Home: "/h"}
	base := "DNS_DOMAIN=example.com\nNATS_ENABLED=true\n"
	for name, tc := range map[string]struct{ global, machine, want string }{
		"no group":   {base, "", "NATS_GROUP: required"},
		"group dots": {base + "NATS_GROUP=a.b\n", "", "NATS_GROUP: 1 to 31"},
		"group case": {base + "NATS_GROUP=Dev\n", "", "NATS_GROUP: 1 to 31"},
		"bad port":   {base + "NATS_GROUP=g\nNATS_PORT=22\n", "", "NATS_PORT"},
		"bad switch": {"DNS_DOMAIN=example.com\nNATS_ENABLED=maybe\n", "", "NATS_ENABLED: true or false"},
	} {
		_, err := settingsfile.Load(setupDir(t, tc.global, tc.machine), "web", env, false)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
	}
	long := strings.Repeat("a", 32)
	if _, err := settingsfile.Load(setupDir(t, base+"NATS_GROUP=g\n", ""), long, env, false); err == nil || !strings.Contains(err.Error(), "Machine name") {
		t.Errorf("a machine name over 31 characters: %v", err)
	}
}

func TestLoadReadsTheNATSMCPSettingsOnlyWhenNATSAndMCPAreOn(t *testing.T) {
	env := settings.Env{Lookup: envOf(nil), Home: "/home/me"}
	base := "DNS_DOMAIN=example.com\nNATS_ENABLED=true\nNATS_GROUP=g\n"
	c, err := settingsfile.Load(setupDir(t, base+"NATS_MCP_PUSH_FROM=a\nNATS_MCP_AUTOALLOW=all\n", ""), "web", env, false)
	if err != nil || c.NATS.MCP || c.NATS.MCPPushFrom != nil || c.NATS.MCPAutoAllow != "off" {
		t.Fatalf("the settings of NATS_MCP are not looked at while it is off: %+v %v", c.NATS, err)
	}
	c, err = settingsfile.Load(setupDir(t, base+"NATS_MCP=true\n", ""), "web", env, false)
	if err != nil || !c.NATS.MCP || len(c.NATS.MCPPushFrom) != 0 || c.NATS.MCPAutoAllow != "off" || c.NATS.MCPBinary != "" {
		t.Fatalf("the defaults: %+v %v", c.NATS, err)
	}
	c, err = settingsfile.Load(setupDir(t, base+"NATS_MCP=1\nNATS_MCP_PUSH_FROM=\" alpha, beta ,alpha,\"\nNATS_MCP_AUTOALLOW=READ\nNATS_MCP_BINARY=~/mcp/bin\n", ""), "web", env, false)
	if err != nil || strings.Join(c.NATS.MCPPushFrom, ",") != "alpha,beta" || c.NATS.MCPAutoAllow != "read" || c.NATS.MCPBinary != "/home/me/mcp/bin" {
		t.Fatalf("%+v %v", c.NATS, err)
	}
}

func TestLoadRefusesBadNATSMCPSettings(t *testing.T) {
	env := settings.Env{Lookup: envOf(nil), Home: "/h"}
	base := "DNS_DOMAIN=example.com\nNATS_ENABLED=true\nNATS_GROUP=g\nNATS_MCP=true\n"
	for name, tc := range map[string]struct{ settings, want string }{
		"bad switch":      {"DNS_DOMAIN=example.com\nNATS_ENABLED=true\nNATS_GROUP=g\nNATS_MCP=maybe\n", "NATS_MCP: true or false"},
		"push from dots":  {base + "NATS_MCP_PUSH_FROM=a.b\n", "NATS_MCP_PUSH_FROM"},
		"push from wild":  {base + "NATS_MCP_PUSH_FROM=a,*\n", "NATS_MCP_PUSH_FROM"},
		"autoallow other": {base + "NATS_MCP_AUTOALLOW=yes\n", "NATS_MCP_AUTOALLOW: off, read or all"},
		"topic dots":      {base + "NATS_MCP_TOPICS=a.b\n", "NATS_MCP_TOPICS"},
		"topic wild":      {base + "NATS_MCP_TOPICS=\"a,>\"\n", "NATS_MCP_TOPICS"},
		"too many topics": {base + "NATS_MCP_TOPICS=t1,t2,t3,t4,t5,t6,t7,t8,t9,t10,t11,t12,t13,t14,t15,t16,t17\n", "at most 16 topics"},
	} {
		_, err := settingsfile.Load(setupDir(t, tc.settings, ""), "web", env, false)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
	}
	// NATS_MCP without NATS is not looked at
	if c, err := settingsfile.Load(setupDir(t, "DNS_DOMAIN=example.com\nNATS_MCP=true\nNATS_MCP_PUSH_FROM=a.b\n", ""), "web", env, false); err != nil || c.NATS.MCP {
		t.Errorf("without NATS_ENABLED nothing is read: %+v %v", c.NATS, err)
	}
}

func TestTheOwnNameInThePushListIsLeftOutSoOneListServesAGroup(t *testing.T) {
	env := settings.Env{Lookup: envOf(nil), Home: "/h"}
	dir := setupDir(t, "DNS_DOMAIN=example.com\nNATS_ENABLED=true\nNATS_GROUP=g\nNATS_MCP=true\nNATS_MCP_PUSH_FROM=alpha,web,beta\n", "")
	c, err := settingsfile.Load(dir, "web", env, false)
	if err != nil || strings.Join(c.NATS.MCPPushFrom, ",") != "alpha,beta" {
		t.Fatalf("%v %v", err, c.NATS.MCPPushFrom)
	}
}

func TestLoadReadsTheDefaultTopicsOfTheMCPServer(t *testing.T) {
	env := settings.Env{Lookup: envOf(nil), Home: "/h"}
	base := "DNS_DOMAIN=example.com\nNATS_ENABLED=true\nNATS_GROUP=g\nNATS_MCP=true\n"
	c, err := settingsfile.Load(setupDir(t, base+"NATS_MCP_TOPICS=\" build, alerts ,build,\"\n", ""), "web", env, false)
	if err != nil || strings.Join(c.NATS.MCPTopics, ",") != "build,alerts" {
		t.Fatalf("%v %v", err, c.NATS.MCPTopics)
	}
	c, err = settingsfile.Load(setupDir(t, "DNS_DOMAIN=example.com\nNATS_MCP_TOPICS=build\n", ""), "web", env, false)
	if err != nil || c.NATS.MCPTopics != nil {
		t.Fatalf("NATS off: %v %v", err, c.NATS.MCPTopics)
	}
}

func TestLoadReadsTheSettingsOfTheKubernetesProvider(t *testing.T) {
	env := settings.Env{Lookup: envOf(nil), Home: "/h"}
	base := "PROVIDER=kubernetes\nDNS_PROVIDER=none\n"
	c, err := settingsfile.Load(setupDir(t, base+"SERVER_LOCATION=k3d-viper\nK8S_IMAGE=viper-machine:v1\nK8S_DISK_GB=30\n", ""), "web", env, false)
	if err != nil {
		t.Fatal(err)
	}
	if c.Provider != "kubernetes" || c.ServerLocation != "k3d-viper" || c.ServerImage != "viper-machine:v1" || c.ServerType != "2x4" || !c.IsContainer() || c.WorkspaceDir != settingsfile.DefaultWorkspace {
		t.Errorf("%+v", c)
	}
	for name, tc := range map[string]struct{ settings, want string }{
		"no cluster and no image":  {base, "Missing settings: SERVER_LOCATION K8S_IMAGE"},
		"no image":                 {base + "SERVER_LOCATION=x\n", "Missing settings: K8S_IMAGE"},
		"DNS":                      {"PROVIDER=kubernetes\nDNS_DOMAIN=example.com\nSERVER_LOCATION=x\nK8S_IMAGE=i\n", "DNS_PROVIDER=none is needed for PROVIDER=kubernetes"},
		"docker mode":              {base + "SERVER_LOCATION=x\nK8S_IMAGE=i\nK8S_DOCKER=podman\n", "K8S_DOCKER: dind, remote or none: podman"},
		"work directory":           {base + "SERVER_LOCATION=x\nK8S_IMAGE=i\nWORKSPACE_DIR=/work\n", "WORKSPACE_DIR: for PROVIDER=kubernetes a directory below /home/builder"},
		"namespace":                {base + "SERVER_LOCATION=x\nK8S_IMAGE=i\nK8S_NAMESPACE=My_Space\n", "K8S_NAMESPACE: the name of a namespace"},
		"docker host":              {base + "SERVER_LOCATION=x\nK8S_IMAGE=i\nK8S_DOCKER=remote\nK8S_DOCKER_HOST=tcp://nohost\n", "K8S_DOCKER_HOST: tcp://host:port"},
		"work directory above":     {base + "SERVER_LOCATION=x\nK8S_IMAGE=i\nWORKSPACE_DIR=/home/builder/../x\n", "WORKSPACE_DIR: for PROVIDER=kubernetes a directory below /home/builder"},
		"pull secret":              {base + "SERVER_LOCATION=x\nK8S_IMAGE=i\nK8S_IMAGE_PULL_SECRET=Reg_Cred\n", "K8S_IMAGE_PULL_SECRET: the name of a Secret"},
		"homebrew volume size":     {base + "SERVER_LOCATION=x\nK8S_IMAGE=i\nK8S_BREW_GB=big\n", "K8S_BREW_GB must be a whole number of GB between 1 and 16384: big"},
		"volume size":              {base + "SERVER_LOCATION=x\nK8S_IMAGE=i\nK8S_DISK_GB=0\n", "K8S_DISK_GB must be a whole number of GB between 1 and 16384: 0"},
		"k3d registry name":        {base + "SERVER_LOCATION=k3d-x\nK8S_IMAGE=i\nK3D_REGISTRY=my_reg\n", "K3D_REGISTRY: the name of a k3d registry"},
		"image as an option":       {base + "SERVER_LOCATION=x\nK8S_IMAGE=-q\n", "K8S_IMAGE: an image reference"},
		"context as an option":     {base + "SERVER_LOCATION=-x\nK8S_IMAGE=i\n", "SERVER_LOCATION: the context of the kubeconfig"},
		"k3d cluster as an option": {base + "SERVER_LOCATION=k3d--x\nK8S_IMAGE=i\n", "SERVER_LOCATION: the context of the kubeconfig"},
		"k3d create":               {base + "SERVER_LOCATION=k3d-x\nK8S_IMAGE=i\nK3D_CLUSTER_CREATE=yes\n", "K3D_CLUSTER_CREATE: 0 or 1: yes"},
		"k3d agents":               {base + "SERVER_LOCATION=k3d-x\nK8S_IMAGE=i\nK3D_AGENTS=99\n", "K3D_AGENTS must be a whole number of agent nodes between 0 and 16: 99"},
	} {
		_, err := settingsfile.Load(setupDir(t, tc.settings, ""), "web", env, false)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
	}
	// NATS groups work with pods (the Service of the group is the address of the server)
	c, err = settingsfile.Load(setupDir(t, base+"SERVER_LOCATION=x\nK8S_IMAGE=i\nNATS_ENABLED=true\nNATS_GROUP=g\n", ""), "web", env, false)
	if err != nil || !c.NATS.Enabled || c.NATS.Group != "g" {
		t.Errorf("NATS: %+v %v", c.NATS, err)
	}
}

func TestLoadReadsTheSettingsOfTheComposeProvider(t *testing.T) {
	env := settings.Env{Lookup: envOf(nil), Home: "/h"}
	base := "PROVIDER=compose\nDNS_PROVIDER=none\n"
	c, err := settingsfile.Load(setupDir(t, base+"SERVER_LOCATION=default\nCOMPOSE_IMAGE=viper-machine:v1\n", ""), "web", env, false)
	if err != nil {
		t.Fatal(err)
	}
	if c.Provider != "compose" || c.ServerLocation != "default" || c.ServerImage != "viper-machine:v1" || c.ServerType != "2x4" || !c.IsContainer() ||
		c.WorkspaceDir != settingsfile.DefaultWorkspace || c.ProxyVerb() != "docker-proxy" || c.DockerMode() != "dind" {
		t.Errorf("%+v", c)
	}
	for name, tc := range map[string]struct{ settings, want string }{
		"no host and no image": {base, "Missing settings: SERVER_LOCATION COMPOSE_IMAGE"},
		"no image":             {base + "SERVER_LOCATION=default\n", "Missing settings: COMPOSE_IMAGE"},
		"context with a dash":  {base + "SERVER_LOCATION=--host=x\nCOMPOSE_IMAGE=i\n", "SERVER_LOCATION: the name of a Docker context"},
		"DNS":                  {"PROVIDER=compose\nDNS_DOMAIN=example.com\nSERVER_LOCATION=x\nCOMPOSE_IMAGE=i\n", "DNS_PROVIDER=none is needed for PROVIDER=compose"},
		"docker mode":          {base + "SERVER_LOCATION=x\nCOMPOSE_IMAGE=i\nCOMPOSE_DOCKER=podman\n", "COMPOSE_DOCKER: dind, remote or none: podman"},
		"remote without host":  {base + "SERVER_LOCATION=x\nCOMPOSE_IMAGE=i\nCOMPOSE_DOCKER=remote\n", "COMPOSE_DOCKER=remote needs COMPOSE_DOCKER_HOST"},
		"docker host":          {base + "SERVER_LOCATION=x\nCOMPOSE_IMAGE=i\nCOMPOSE_DOCKER=remote\nCOMPOSE_DOCKER_HOST=tcp://nohost\n", "COMPOSE_DOCKER_HOST: tcp://host:port"},
		"image":                {base + "SERVER_LOCATION=x\nCOMPOSE_IMAGE=-bad\n", "COMPOSE_IMAGE: an image reference"},
		"work directory":       {base + "SERVER_LOCATION=x\nCOMPOSE_IMAGE=i\nWORKSPACE_DIR=/work\n", "WORKSPACE_DIR: for PROVIDER=compose a directory below /home/builder"},
		"swap":                 {base + "SERVER_LOCATION=x\nCOMPOSE_IMAGE=i\nVIPER_SWAP=1\n", "VIPER_SWAP"},
	} {
		_, err := settingsfile.Load(setupDir(t, tc.settings, ""), "web", env, false)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
	}
}

// The settings of a k3d cluster are settings like the others: a machine file beats its group file, which beats .env.local,
// and what no file names comes from the environment.
func TestK3dSettingsComeFromTheMachineTheGroupAndTheEnvironment(t *testing.T) {
	base := "PROVIDER=kubernetes\nDNS_PROVIDER=none\nSERVER_LOCATION=k3d-viper\nK8S_IMAGE=i\n"
	dir := setupDir(t, base+"K3D_REGISTRY=fromdotenv\n", "K3D_AGENTS=3\n")
	if err := os.MkdirAll(filepath.Join(dir, "groups"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "groups", "dev.env"), []byte("GROUP_MACHINES=web\nK3D_REGISTRY=fromgroup\nK3D_CLUSTER_CREATE=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := settings.Env{Lookup: envOf(map[string]string{"K3D_AGENTS": "9", "K3D_REGISTRY": "fromshell"}), Home: "/h"}
	c, err := settingsfile.Load(dir, "web", env, false)
	if err != nil {
		t.Fatal(err)
	}
	if c.Get("K3D_REGISTRY") != "fromgroup" || c.Get("K3D_CLUSTER_CREATE") != "1" || c.Get("K3D_AGENTS") != "3" {
		t.Errorf("registry %q, create %q, agents %q", c.Get("K3D_REGISTRY"), c.Get("K3D_CLUSTER_CREATE"), c.Get("K3D_AGENTS"))
	}
	// only the shell: it counts too
	dir = setupDir(t, base, "")
	c, err = settingsfile.Load(dir, "web", env, false)
	if err != nil || c.Get("K3D_REGISTRY") != "fromshell" || c.Get("K3D_AGENTS") != "9" {
		t.Errorf("%v %q %q", err, c.Get("K3D_REGISTRY"), c.Get("K3D_AGENTS"))
	}
}

// A release of viper has a machine image published under its version, which is the default of K8S_IMAGE and COMPOSE_IMAGE; a
// build that is not a release has none, and a file or the environment beats it.
func TestTheImageOfAReleaseIsTheDefaultOfK8sImageAndComposeImage(t *testing.T) {
	for provider, setting := range map[string]string{"kubernetes": "K8S_IMAGE", "compose": "COMPOSE_IMAGE"} {
		base := "PROVIDER=" + provider + "\nDNS_PROVIDER=none\nSERVER_LOCATION=x\n"
		env := func(version string, vars map[string]string) settings.Env {
			return settings.Env{Lookup: envOf(vars), Home: "/h", Version: version}
		}
		c, err := settingsfile.Load(setupDir(t, base, ""), "web", env("v0.18.0", nil), false)
		if err != nil || c.ServerImage != "ghcr.io/pt9912/viper-machine:v0.18.0" || c.Get(setting) != c.ServerImage {
			t.Errorf("%s: a release takes its own image: %q %v", provider, c.ServerImage, err)
		}
		for _, v := range []string{"dev", "v0.18.0-3-gabc1234", "v0.18.0-dirty", ""} {
			if _, err := settingsfile.Load(setupDir(t, base, ""), "web", env(v, nil), false); err == nil || !strings.Contains(err.Error(), "Missing settings: "+setting) {
				t.Errorf("%s, version %q: %v", provider, v, err)
			}
		}
		c, err = settingsfile.Load(setupDir(t, base+setting+"=mine:1\n", ""), "web", env("v0.18.0", nil), false)
		if err != nil || c.ServerImage != "mine:1" {
			t.Errorf("%s: a file names the image: %q %v", provider, c.ServerImage, err)
		}
		c, err = settingsfile.Load(setupDir(t, base, ""), "web", env("v0.18.0", map[string]string{setting: "fromshell:2"}), false)
		if err != nil || c.ServerImage != "fromshell:2" {
			t.Errorf("%s: the environment names the image: %q %v", provider, c.ServerImage, err)
		}
	}
	// another provider is not given an image
	c, err := settingsfile.Load(setupDir(t, "DNS_DOMAIN=example.com\n", ""), "web", settings.Env{Lookup: envOf(nil), Home: "/h", Version: "v0.18.0"}, false)
	if err != nil || c.Get("K8S_IMAGE") != "" || c.Get("COMPOSE_IMAGE") != "" {
		t.Errorf("%v %q", err, c.Get("K8S_IMAGE"))
	}
}
