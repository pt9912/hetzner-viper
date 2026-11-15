package settingsfile_test

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/settingsfile"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

func restoreOf(name string) settings.Change { return settings.Change{Kind: "restore", Name: name} }

// writeZip makes a backup the way a stranger might: any names, any modes.
func writeZip(t *testing.T, env settings.Env, name string, entries map[string]string, mode map[string]os.FileMode) {
	t.Helper()
	dir := filepath.Join(env.Home, ".local", "state", "viper", "backups")
	_ = os.MkdirAll(dir, 0o700)
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for n, text := range entries {
		h := &zip.FileHeader{Name: n, Method: zip.Deflate}
		if m, ok := mode[n]; ok {
			h.SetMode(m)
		}
		w, _ := zw.CreateHeader(h)
		_, _ = w.Write([]byte(text))
	}
	_ = zw.Close()
	_ = f.Close()
}

func TestABackupIsPutBackAndTheCredentialsThatAreThereStay(t *testing.T) {
	env, _ := credEnv(t)
	text := "PROVIDER=hetzner\nSERVER_TYPE=cpx22\nHCLOUD_TOKEN=$MYTOKEN\nVIPER_GH_TOKEN='ghp_current_value_1'\n"
	dir := setupDir(t, "SERVER_PREFIX=claude\nDNS_PROVIDER=none\n", "")
	writeIn(t, dir, "machines/web.env.local", text)
	first, err := apply(t, dir, env, setting(map[string]string{"SERVER_TYPE": "cpx32"}))
	if err != nil {
		t.Fatal(err)
	}
	// a credential is replaced after the backup: the restore must not bring the old (empty) one back
	if _, err := setCred(t, dir, env, settings.Change{Kind: "credential", Target: "machine", Name: "web", Fields: map[string]string{"VIPER_GH_TOKEN": "ghp_newer_value_22"}}); err != nil {
		t.Fatal(err)
	}
	p, err := settingsfile.Files{}.Preview(dir, restoreOf(first.Backup), env)
	if err != nil || len(p.Problems) > 0 || len(p.Broken) > 0 {
		t.Fatalf("%v %+v", err, p)
	}
	if len(p.Files) != 1 || !strings.Contains(p.Files[0].Text, "~ SERVER_TYPE: cpx32 -> cpx22") || strings.Contains(p.Files[0].Text, "ghp_") {
		t.Errorf("preview %+v", p.Files)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "machines", "web.env.local")); strings.Contains(string(b), "cpx22") {
		t.Fatal("the preview wrote")
	}
	res, err := settingsfile.Files{}.Create(dir, settings.Change{Kind: "restore", Name: first.Backup, Etag: p.Etag}, env)
	if err != nil {
		t.Fatalf("%v %+v", err, res)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "machines", "web.env.local"))
	want := "PROVIDER=hetzner\nSERVER_TYPE=cpx22\nHCLOUD_TOKEN=$MYTOKEN\nVIPER_GH_TOKEN='ghp_newer_value_22'\n"
	if string(got) != want {
		t.Errorf("restored:\n%s\nwant:\n%s", got, want)
	}
	if res.Backup == "" || res.Backup == first.Backup {
		t.Errorf("a restore keeps a copy of what it replaces: %q", res.Backup)
	}
	// the same again: nothing differs
	p, _ = settingsfile.Files{}.Preview(dir, restoreOf(first.Backup), env)
	if !strings.Contains(strings.Join(p.Problems, "|"), "the same as in the backup") {
		t.Errorf("%+v", p)
	}
}

func TestOnlyABackupThatLooksLikeOursIsRead(t *testing.T) {
	env, _ := credEnv(t)
	dir := setupDir(t, "SERVER_PREFIX=claude\nDNS_PROVIDER=none\n", "PROVIDER=hetzner\n")
	big := strings.Repeat("A=1\n", 100000)
	for name, c := range map[string]struct {
		entries map[string]string
		mode    map[string]os.FileMode
		want    string
	}{
		"traversal":       {map[string]string{"../x.env.local": "A=1\n"}, nil, "not the name of a settings file"},
		"absolute":        {map[string]string{"/etc/passwd": "A=1\n"}, nil, "not the name"},
		"other name":      {map[string]string{"machines/Web.env.local": "A=1\n"}, nil, "not the name"},
		"example":         {map[string]string{"machines/web.env.local.bak": "A=1\n"}, nil, "not the name"},
		"link":            {map[string]string{".env.local": "x"}, map[string]os.FileMode{".env.local": os.ModeSymlink | 0o777}, "not a plain file"},
		"large":           {map[string]string{".env.local": big}, nil, "larger"},
		"credential":      {map[string]string{".env.local": "HCLOUD_TOKEN='abcdefgh1234'\n"}, nil, "holds no credentials"},
		"carriage return": {map[string]string{".env.local": "A=1\r\n"}, nil, "Unix line ends"},
		"not utf8":        {map[string]string{".env.local": "A=\xff\n"}, nil, "not text"},
		"unreadable":      {map[string]string{".env.local": "A=$(rm -rf x)\n"}, nil, "unreadable"},
		"nothing":         {map[string]string{"manifest.yaml": "x"}, nil, "no file"},
	} {
		zname := "20260101T000000-restore.zip"
		writeZip(t, env, zname, c.entries, c.mode)
		p, _ := settingsfile.Files{}.Preview(dir, restoreOf(zname), env)
		if !strings.Contains(strings.Join(p.Problems, "|"), c.want) {
			t.Errorf("%s: %q", name, p.Problems)
		}
		if _, err := (settingsfile.Files{}).Create(dir, settings.Change{Kind: "restore", Name: zname, Etag: p.Etag}, env); err == nil {
			t.Errorf("%s: restored", name)
		}
	}
	for _, bad := range []string{"../x.zip", "x.zip", "nope", ""} {
		p, _ := settingsfile.Files{}.Preview(dir, restoreOf(bad), env)
		if !strings.Contains(strings.Join(p.Problems, "|"), "no such backup") {
			t.Errorf("%q: %q", bad, p.Problems)
		}
	}
}

func TestARestoreIsCheckedLikeAnyChange(t *testing.T) {
	env, _ := credEnv(t)
	dir := setupDir(t, "SERVER_PREFIX=claude\nDNS_PROVIDER=none\n", compose)
	// the copy would make the machine a hetzner one without a token: the check of every machine says so only for tokens it needs
	zname := "20260102T000000-setting.zip"
	writeZip(t, env, zname, map[string]string{"machines/web.env.local": "PROVIDER=compose\nSERVER_LOCATION=default\nCOMPOSE_IMAGE=x:1\nDNS_PROVIDER=cloudflare\n"}, nil)
	p, _ := settingsfile.Files{}.Preview(dir, restoreOf(zname), env)
	if len(p.Problems)+len(p.Broken) == 0 {
		t.Errorf("a file that does not load was accepted: %+v", p)
	}
	// a command that runs on the server asks for the word
	zname = "20260103T000000-setting.zip"
	writeZip(t, env, zname, map[string]string{"machines/web.env.local": compose + "VIPER_POST_CLONE='make all'\n"}, nil)
	p, _ = settingsfile.Files{}.Preview(dir, restoreOf(zname), env)
	if p.ConfirmWord != "restore" || len(p.Problems) != 0 {
		t.Fatalf("%+v", p)
	}
	if _, err := (settingsfile.Files{}).Create(dir, settings.Change{Kind: "restore", Name: zname, Etag: p.Etag}, env); err == nil {
		t.Error("restored without the word")
	}
	if _, err := (settingsfile.Files{}).Create(dir, settings.Change{Kind: "restore", Name: zname, Etag: p.Etag, Confirm: "restore"}, env); err != nil {
		t.Fatal(err)
	}
	// the file changed after the preview
	zname = "20260104T000000-setting.zip"
	writeZip(t, env, zname, map[string]string{"machines/web.env.local": compose}, nil)
	p, _ = settingsfile.Files{}.Preview(dir, restoreOf(zname), env)
	_ = os.WriteFile(filepath.Join(dir, "machines", "web.env.local"), []byte(compose+"# changed\n"), 0o600)
	if _, err := (settingsfile.Files{}).Create(dir, settings.Change{Kind: "restore", Name: zname, Etag: p.Etag}, env); err == nil {
		t.Error("a file that changed was replaced")
	}
	// a file that is gone is not made again
	_ = os.Remove(filepath.Join(dir, "machines", "web.env.local"))
	p, _ = settingsfile.Files{}.Preview(dir, restoreOf(zname), env)
	if !strings.Contains(strings.Join(p.Problems, "|"), "make it first") {
		t.Errorf("%+v", p)
	}
}

func TestARestoreDoesNotMoveAMachineThatHasAServer(t *testing.T) {
	env, home := credEnv(t)
	dir := setupDir(t, "SERVER_PREFIX=claude\nDNS_PROVIDER=none\n", compose)
	marks := filepath.Join(env.Home, ".local", "state", "viper", "servers", "claude")
	_ = os.MkdirAll(marks, 0o700)
	_ = os.WriteFile(filepath.Join(marks, "id-claude-web"), []byte("1\n"), 0o600)
	_ = home
	zname := "20260105T000000-setting.zip"
	writeZip(t, env, zname, map[string]string{"machines/web.env.local": strings.Replace(compose, "SERVER_LOCATION=default", "SERVER_LOCATION=other", 1)}, nil)
	p, _ := settingsfile.Files{}.Preview(dir, restoreOf(zname), env)
	if !strings.Contains(strings.Join(p.Problems, "|"), "viper down first") {
		t.Errorf("%+v", p)
	}
}
