package settingsfile_test

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/settingsfile"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

const compose = "# my machine\nPROVIDER=compose\nSERVER_LOCATION=default\nSERVER_TYPE=2x4   # small\nCOMPOSE_IMAGE=x:1\nDNS_PROVIDER=none\n"

func setting(f map[string]string, rm ...string) settings.Change {
	return settings.Change{Kind: "setting", Target: "machine", Name: "web", Fields: f, Remove: rm}
}

func settingDir(t *testing.T) (string, settings.Env) {
	t.Helper()
	env, _ := credEnv(t)
	return setupDir(t, "SERVER_PREFIX=claude\nDNS_PROVIDER=none\n", compose), env
}

func apply(t *testing.T, dir string, env settings.Env, ch settings.Change) (settings.Preview, error) {
	t.Helper()
	p, err := settingsfile.Files{}.Preview(dir, ch, env)
	if err != nil || len(p.Problems) > 0 {
		t.Fatalf("preview: %v %+v", err, p)
	}
	ch.Etag = p.Etag
	if p.ConfirmWord != "" {
		ch.Confirm = p.ConfirmWord
	}
	return settingsfile.Files{}.Create(dir, ch, env)
}

func TestASettingIsChangedInPlaceWithItsOldAndNewValue(t *testing.T) {
	dir, env := settingDir(t)
	p, err := apply(t, dir, env, setting(map[string]string{"SERVER_TYPE": "4x8"}))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "machines", "web.env.local"))
	if string(got) != strings.Replace(compose, "SERVER_TYPE=2x4", "SERVER_TYPE='4x8'", 1) {
		t.Errorf("%q", got)
	}
	if len(p.Files) != 1 || !strings.Contains(p.Files[0].Text, "~ SERVER_TYPE: 2x4 -> 4x8") || p.Backup == "" {
		t.Errorf("%+v", p)
	}
	bs, err := settingsfile.Files{}.Backups(env)
	if err != nil || len(bs) != 1 || bs[0].Name != p.Backup || strings.Join(bs[0].Files, ",") != "machines/web.env.local" {
		t.Fatalf("%v %v", bs, err)
	}
	rc, err := settingsfile.Files{}.OpenBackup(env, bs[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()
	for _, bad := range []string{"../x.zip", "x", "20260101T000000-setting.zip/../../etc", ""} {
		if _, err := (settingsfile.Files{}).OpenBackup(env, bad); err == nil {
			t.Errorf("%q opened", bad)
		}
	}
	// the copy holds the file as it was
	rc, _ = settingsfile.Files{}.OpenBackup(env, bs[0].Name)
	b, _ := io.ReadAll(rc)
	_ = rc.Close()
	if len(b) == 0 {
		t.Error("an empty backup")
	}
}

func TestOnlyTheSettingsOfTheListAndOnlyValuesOfTheirKind(t *testing.T) {
	dir, env := settingDir(t)
	for name, c := range map[string]struct {
		ch   settings.Change
		want string
	}{
		"credential":      {setting(map[string]string{"HCLOUD_TOKEN": "abcdefgh123"}), "not a setting this page changes"},
		"lines empty":     {setting(map[string]string{"VIPER_POST_CLONE": "  \n "}), "empty"},
		"lines control":   {setting(map[string]string{"VIPER_POST_CLONE": "a\rb"}), "no other control character"},
		"on error":        {setting(map[string]string{"VIPER_POST_CLONE_ON_ERROR": "explode"}), "one of retry, ignore, abort"},
		"unknown":         {setting(map[string]string{"NOPE": "x"}), "not a setting"},
		"enum":            {setting(map[string]string{"NATS_ENABLED": "yes"}), "one of true, false"},
		"number":          {setting(map[string]string{"NATS_PORT": "70000"}), "a number from 1024 to 65535"},
		"number text":     {setting(map[string]string{"NATS_PORT": "12x"}), "a number"},
		"url credentials": {setting(map[string]string{"REPO_URL": "https://u:tok@h.example/r.git"}), "credentials"},
		"dash":            {setting(map[string]string{"REPO_URL": "--upload-pack=x"}), "must not start"},
		"line":            {setting(map[string]string{"SERVER_TYPE": "a\nB=1"}), "one line"},
		"nothing":         {setting(nil), "nothing to change"},
		"both":            {setting(map[string]string{"SERVER_TYPE": "x"}, "SERVER_TYPE"), "at once"},
		"prefix":          {setting(map[string]string{"SERVER_PREFIX": "Bad Name"}), "lowercase"},
	} {
		p, _ := settingsfile.Files{}.Preview(dir, c.ch, env)
		if !strings.Contains(strings.Join(p.Problems, "|"), c.want) {
			t.Errorf("%s: %q", name, p.Problems)
		}
		if _, err := (settingsfile.Files{}).Create(dir, c.ch, env); err == nil {
			t.Errorf("%s: written", name)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "machines", "web.env.local")); string(b) != compose {
		t.Errorf("the file changed: %q", b)
	}
}

func TestADangerousSettingAsksToTypeTheNameAndANewValueIsSetOrTakenOut(t *testing.T) {
	dir, env := settingDir(t)
	ch := setting(map[string]string{"COMPOSE_DOCKER": "dind"})
	p, _ := settingsfile.Files{}.Preview(dir, ch, env)
	if p.ConfirmWord != "web" || len(p.Confirm) != 1 || !strings.Contains(p.Confirm[0], "privileged") {
		t.Fatalf("%+v", p)
	}
	ch.Etag = p.Etag
	if _, err := (settingsfile.Files{}).Create(dir, ch, env); err == nil {
		t.Fatal("written without the confirmation")
	}
	ch.Confirm = "web"
	if _, err := (settingsfile.Files{}).Create(dir, ch, env); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "machines", "web.env.local"))
	if !strings.Contains(string(b), "COMPOSE_DOCKER='dind'\n") {
		t.Errorf("%q", b)
	}
	// none is not dangerous; taking it out is
	p, _ = settingsfile.Files{}.Preview(dir, setting(map[string]string{"COMPOSE_DOCKER": "none"}), env)
	if p.ConfirmWord != "" {
		t.Errorf("%+v", p)
	}
	if _, err := apply(t, dir, env, setting(nil, "COMPOSE_DOCKER")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "machines", "web.env.local")); strings.Contains(string(b), "COMPOSE_DOCKER") {
		t.Errorf("%q", b)
	}
}

func TestAChangeThatBreaksAnotherMachineIsRefusedAndAServerBlocksTheSettingsViperDownNeeds(t *testing.T) {
	dir, env := settingDir(t)
	writeIn(t, dir, "machines/db.env.local", "PROVIDER=compose\nSERVER_LOCATION=default\nCOMPOSE_IMAGE=x:1\n")
	// .env.local gives DNS_PROVIDER=none; taking the machine's own none out, db inherits it: fine. Setting cloudflare breaks all
	p, err := settingsfile.Files{}.Create(dir, settings.Change{Kind: "setting", Target: "machine", Name: "db", Fields: map[string]string{"DNS_PROVIDER": "cloudflare"}, Etag: ""}, env)
	if err == nil {
		t.Fatalf("%+v", p)
	}
	pp, _ := settingsfile.Files{}.Preview(dir, settings.Change{Kind: "setting", Target: "machine", Name: "db", Fields: map[string]string{"DNS_PROVIDER": "cloudflare"}}, env)
	if len(pp.Problems) == 0 && len(pp.Broken) == 0 {
		t.Errorf("a compose machine with cloudflare DNS loads: %+v", pp)
	}
	// a server: its location and provider stay
	home := env.Home
	marks := filepath.Join(home, ".local", "state", "viper", "servers", "claude")
	_ = os.MkdirAll(marks, 0o700)
	_ = os.WriteFile(filepath.Join(marks, "id-claude-web"), []byte("1\n"), 0o600)
	pp, _ = settingsfile.Files{}.Preview(dir, setting(map[string]string{"SERVER_LOCATION": "other"}), env)
	if !strings.Contains(strings.Join(pp.Problems, "|"), "viper down first") {
		t.Errorf("%+v", pp)
	}
	pp, _ = settingsfile.Files{}.Preview(dir, setting(map[string]string{"SERVER_TYPE": "4x8"}), env)
	if len(pp.Problems) != 0 || !strings.Contains(strings.Join(pp.Notes, "|"), "next viper up") {
		t.Errorf("a setting that does not move the server: %+v", pp)
	}
	// the base file reaches the machine with a server too
	pp, _ = settingsfile.Files{}.Preview(dir, settings.Change{Kind: "setting", Target: "base", Fields: map[string]string{"SERVER_PREFIX": "other"}}, env)
	if !strings.Contains(strings.Join(pp.Problems, "|"), "web") {
		t.Errorf("%+v", pp)
	}
}

func TestEverySettingOfTheCatalogHasAHelpInBothLanguagesAndTheHelpNamesNothingElse(t *testing.T) {
	names := map[string]bool{}
	for _, s := range (settingsfile.Files{}).Catalog() {
		names[s.Name] = true
		if len(s.Help["en"]) < 20 || len(s.Help["de"]) < 20 || s.Help["en"] == s.Help["de"] {
			t.Errorf("%s: help %q", s.Name, s.Help)
		}
		if strings.HasSuffix(s.Help["en"], " ") || strings.Contains(s.Help["en"]+s.Help["de"], "\n") {
			t.Errorf("%s: the help is one line of text", s.Name)
		}
	}
	for _, n := range settingsfile.HelpNames() {
		if !names[n] {
			t.Errorf("a help for %s, which the catalog does not offer", n)
		}
	}
}

func markServer(t *testing.T, env settings.Env, prefix, machine string) {
	t.Helper()
	marks := filepath.Join(env.Home, ".local", "state", "viper", "servers", prefix)
	_ = os.MkdirAll(marks, 0o700)
	_ = os.WriteFile(filepath.Join(marks, "id-"+prefix+"-"+machine), []byte("1\n"), 0o600)
}

func problemsOf(t *testing.T, dir string, env settings.Env, ch settings.Change) string {
	t.Helper()
	p, err := settingsfile.Files{}.Preview(dir, ch, env)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(p.Problems, "|")
}

func TestAServerIsFoundWhereverTheSettingsReachTheMachine(t *testing.T) {
	env, _ := credEnv(t)
	dir := setupDir(t, "SERVER_PREFIX=claude\nDNS_PROVIDER=none\n", compose)
	writeIn(t, dir, "groups/g1.env", "GROUP_MACHINES=c\n")
	writeIn(t, dir, "machines/c.env.local", "PROVIDER=compose\nSERVER_LOCATION=default\nCOMPOSE_IMAGE=x:1\n")
	markServer(t, env, "claude", "c")
	// a machine that only a group names, through the base file
	base := settings.Change{Kind: "setting", Target: "base", Fields: map[string]string{"SERVER_LOCATION": "other"}}
	if !strings.Contains(problemsOf(t, dir, env, base), "viper down first") {
		t.Error("the base file moves a machine that has a server")
	}
	// the group takes a machine that has a server
	grp := settings.Change{Kind: "setting", Target: "group", Name: "g1", Fields: map[string]string{"GROUP_MACHINES": "c,web"}}
	if !strings.Contains(problemsOf(t, dir, env, grp), "viper down first") {
		t.Error("the membership of a group with a server")
	}
	// the DNS name is deleted by the name the settings say
	dns := settings.Change{Kind: "setting", Target: "machine", Name: "c", Fields: map[string]string{"DNS_DOMAIN": "example.org"}}
	if !strings.Contains(problemsOf(t, dir, env, dns), "viper down first") {
		t.Error("DNS_DOMAIN of a machine with a server")
	}
	// a machine that two groups name cannot be loaded: the mark is still found
	writeIn(t, dir, "groups/g2.env", "GROUP_MACHINES=c\n")
	if !strings.Contains(problemsOf(t, dir, env, settings.Change{Kind: "setting", Target: "machine", Name: "c", Fields: map[string]string{"SERVER_LOCATION": "other"}}), "viper down first") {
		t.Error("a machine in two groups")
	}
}

func TestValuesAreCheckedLikePreflightChecksThem(t *testing.T) {
	dir, env := settingDir(t)
	for _, c := range []struct {
		name, value string
		ok          bool
	}{
		{"REPO_URL", "ssh://git@github.com/o/r.git", true}, {"REPO_URL", "git@github.com:o/r.git", true}, {"REPO_URL", "https://h.example/x?a=b@c", true},
		{"REPO_URL", "https://u:tok@h.example/x.git", false}, {"REPO_URL", "https://tok@h.example/x.git", false},
		{"SSH_ALLOWED_CIDRS", "10.0.0.0/8,::/0", true}, {"SSH_ALLOWED_CIDRS", "junk", false}, {"SSH_ALLOWED_CIDRS", "10.0.0.0/8,evil", false},
		{"VIPER_SWAP", "0", true}, {"VIPER_SWAP", "0.5", true}, {"VIPER_SWAP", "4G", true}, {"VIPER_SWAP", "9", false}, {"VIPER_SWAP", "lots", false},
		{"VIPER_CODE_SERVER", "1", true}, {"VIPER_CODE_SERVER", "yes", false}, {"SERVER_TYPE", "", false},
	} {
		got := problemsOf(t, dir, env, setting(map[string]string{c.name: c.value}))
		if (got == "") != c.ok {
			t.Errorf("%s=%q: ok %v, problems %q", c.name, c.value, c.ok, got)
		}
	}
}

func TestTheOldValueIsOnlySaidWhenTheFileSaysItPlainly(t *testing.T) {
	dir, env := settingDir(t)
	writeIn(t, dir, "machines/web.env.local", compose+"WORKSPACE_DIR=$HOME/work\nSERVER_IMAGE=ubuntu-24.04\n")
	p, _ := settingsfile.Files{}.Preview(dir, setting(map[string]string{"SERVER_IMAGE": "ubuntu-26.04"}), env)
	if !strings.Contains(p.Files[0].Text, "~ SERVER_IMAGE: ubuntu-24.04 -> ubuntu-26.04") {
		t.Errorf("%q", p.Files[0].Text)
	}
	p, _ = settingsfile.Files{}.Preview(dir, setting(map[string]string{"WORKSPACE_DIR": "/home/builder/w"}), env)
	if !strings.Contains(p.Files[0].Text, "WORKSPACE_DIR: (set) -> /home/builder/w") {
		t.Errorf("%q", p.Files[0].Text)
	}
}

func TestTwoBackupsInOneSecondAreListedNewestFirst(t *testing.T) {
	dir, env := settingDir(t)
	a, err := apply(t, dir, env, setting(map[string]string{"SERVER_TYPE": "4x8"}))
	if err != nil {
		t.Fatal(err)
	}
	b, err := apply(t, dir, env, setting(map[string]string{"SERVER_TYPE": "8x16"}))
	if err != nil {
		t.Fatal(err)
	}
	list, _ := settingsfile.Files{}.Backups(env)
	if len(list) != 2 || list[0].Name != b.Backup || list[1].Name != a.Backup {
		t.Errorf("%v (a %s, b %s)", list, a.Backup, b.Backup)
	}
}

func TestCommandsOverSeveralLinesAreWrittenSafelyAndAskForTheName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no bash")
	}
	dir, env := settingDir(t)
	cmd := "npm ci\nexport X='it'\"'s'\nmake \"$HOME\" `date` $(id) \\ ; echo done\n\tindented"
	ch := setting(map[string]string{"VIPER_POST_CLONE": cmd})
	p, _ := settingsfile.Files{}.Preview(dir, ch, env)
	if p.ConfirmWord != "web" || len(p.Confirm) != 1 || !strings.Contains(p.Confirm[0], "shell commands") || !strings.Contains(p.Files[0].Text, "+ VIPER_POST_CLONE = npm ci") {
		t.Fatalf("%+v", p)
	}
	ch.Etag = p.Etag
	if _, err := (settingsfile.Files{}).Create(dir, ch, env); err == nil {
		t.Fatal("written without the name")
	}
	ch.Confirm = "web"
	if _, err := (settingsfile.Files{}).Create(dir, ch, env); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "machines", "web.env.local")
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	out, err := exec.Command(bash, "-c", `source "$1"; printf %s "$VIPER_POST_CLONE"`, "_", f).Output()
	if err != nil || string(out) != cmd {
		t.Errorf("bash reads %q (%v)", out, err)
	}
	m, err := settingsfile.Load(dir, "web", env, false)
	if err != nil || m.Get("VIPER_POST_CLONE") != cmd {
		t.Errorf("viper reads %q (%v)", m.Get("VIPER_POST_CLONE"), err)
	}
	// reading it back for the page, and nothing else
	got, err := settingsfile.Files{}.ReadSetting(dir, "machine", "web", "VIPER_POST_CLONE", env)
	if err != nil || got != cmd {
		t.Errorf("%q %v", got, err)
	}
	for _, other := range []string{"SERVER_TYPE", "HCLOUD_TOKEN", "REPO_URL", "NOPE"} {
		if _, err := (settingsfile.Files{}).ReadSetting(dir, "machine", "web", other, env); err == nil {
			t.Errorf("%s was given out", other)
		}
	}
	if _, err := (settingsfile.Files{}).ReadSetting(dir, "machine", "../x", "VIPER_POST_CLONE", env); err == nil {
		t.Error("a path was read")
	}
	// the preview of a change says the old commands, and taking them out asks for the name as well
	p, _ = settingsfile.Files{}.Preview(dir, setting(map[string]string{"VIPER_POST_CLONE": "make"}), env)
	if !strings.Contains(p.Files[0].Text, "~ VIPER_POST_CLONE: npm ci\n      export") || p.ConfirmWord != "web" {
		t.Errorf("%+v", p)
	}
	p, _ = settingsfile.Files{}.Preview(dir, setting(nil, "VIPER_POST_CLONE"), env)
	if p.ConfirmWord != "web" {
		t.Errorf("%+v", p)
	}
}

func TestWhatTheShellExpandsIsFoundOutsideSingleQuotes(t *testing.T) {
	dir, env := settingDir(t)
	writeIn(t, dir, "machines/web.env.local", compose+"SERVER_IMAGE='$HOME'\nWORKSPACE_DIR=$HOME/w\n")
	p, _ := settingsfile.Files{}.Preview(dir, setting(map[string]string{"SERVER_IMAGE": "x"}), env)
	if !strings.Contains(p.Files[0].Text, "SERVER_IMAGE: $HOME -> x") {
		t.Errorf("a $ in single quotes is a letter: %q", p.Files[0].Text)
	}
	p, _ = settingsfile.Files{}.Preview(dir, setting(map[string]string{"WORKSPACE_DIR": "/home/builder/x"}), env)
	if !strings.Contains(p.Files[0].Text, "WORKSPACE_DIR: (set) ->") {
		t.Errorf("%q", p.Files[0].Text)
	}
}

func TestAnExpansionIsFoundByTheQuotesTheShellReads(t *testing.T) {
	dir, env := settingDir(t)
	for raw, expands := range map[string]bool{
		`SERVER_IMAGE='$HOME'`:       false,
		`SERVER_IMAGE="it's $HOME"`:  true,
		`SERVER_IMAGE="it's"`:        false,
		`SERVER_IMAGE='say "$HOME"'`: false,
		`SERVER_IMAGE=\$HOME`:        false,
		`SERVER_IMAGE="a \$HOME"`:    false,
		`SERVER_IMAGE=~/x`:           true,
		`SERVER_IMAGE=a~b`:           false,
	} {
		writeIn(t, dir, "machines/web.env.local", compose+raw+"\n")
		p, _ := settingsfile.Files{}.Preview(dir, setting(map[string]string{"SERVER_IMAGE": "x"}), env)
		if len(p.Files) == 0 {
			t.Errorf("%s: %v", raw, p.Problems)
			continue
		}
		got := strings.Contains(p.Files[0].Text, "SERVER_IMAGE: (set) ->")
		if got != expands { // a value that expands is said as "(set)"; one that does not is said as it is
			t.Errorf("%s: shown as %q (an expansion is hidden, a plain value is not)", raw, p.Files[0].Text)
		}
	}
}

func TestACommandWithACredentialInAUrlIsRefused(t *testing.T) {
	dir, env := settingDir(t)
	got := problemsOf(t, dir, env, setting(map[string]string{"VIPER_POST_CLONE": "git clone https://u:tok123456@h.example/x.git\nmake"}))
	if !strings.Contains(got, "a URL with credentials") {
		t.Errorf("%q", got)
	}
	if got := problemsOf(t, dir, env, setting(map[string]string{"VIPER_POST_CLONE": "git clone ssh://git@github.com/o/r.git\nmake"})); got != "" {
		t.Errorf("ssh with a user: %q", got)
	}
}
