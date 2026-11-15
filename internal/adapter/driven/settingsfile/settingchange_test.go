package settingsfile_test

import (
	"io"
	"os"
	"path/filepath"
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
		"post clone":      {setting(map[string]string{"VIPER_POST_CLONE": "rm -rf /"}), "not a setting this page changes"},
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
