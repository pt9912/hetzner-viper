package settingsfile_test

import (
	"archive/zip"
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

const base = "# my settings\nexport SERVER_PREFIX=claude   # the prefix\nDNS_PROVIDER=none\nHCLOUD_TOKEN='old-token-value-1'\n\nPROVIDER=hetzner\n"

func credEnv(t *testing.T) (settings.Env, string) {
	t.Helper()
	home := t.TempDir()
	return settings.Env{Lookup: envOf(nil), Home: home, Version: "v0.0.0"}, home
}

func credChange(f map[string]string, rm ...string) settings.Change {
	return settings.Change{Kind: "credential", Target: "base", Fields: f, Remove: rm}
}

func setCred(t *testing.T, dir string, env settings.Env, ch settings.Change) (settings.Preview, error) {
	t.Helper()
	p, err := settingsfile.Files{}.Preview(dir, ch, env)
	if err != nil || len(p.Problems) > 0 {
		t.Fatalf("preview: %v %+v", err, p)
	}
	ch.Etag = p.Etag
	return settingsfile.Files{}.Create(dir, ch, env)
}

func TestAssignmentsGiveTheSpanOfEachStatement(t *testing.T) {
	src := "export A=1 # c\nB='x y'\nC=\"two\nlines\" D=4\n"
	as, err := settingsfile.Assignments(src)
	if err != nil || len(as) != 4 {
		t.Fatalf("%v %v", as, err)
	}
	got := []string{}
	for _, a := range as {
		got = append(got, src[a.Start:a.End])
	}
	if strings.Join(got, "|") != "A=1|B='x y'|C=\"two\nlines\"|D=4" {
		t.Errorf("%q", got)
	}
	if _, err := settingsfile.Assignments("A=1\r\n"); err == nil {
		t.Error("a Windows line end was accepted")
	}
	if _, err := settingsfile.Assignments("A=$(rm -rf x)\n"); err == nil {
		t.Error("a command substitution was accepted")
	}
}

func TestACredentialIsReplacedInItsPlaceAndNothingElseChanges(t *testing.T) {
	env, home := credEnv(t)
	dir := setupDir(t, base, "")
	const value = "new-secret-value-it's-2"
	p, err := setCred(t, dir, env, credChange(map[string]string{"HCLOUD_TOKEN": value}))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, ".env.local"))
	want := strings.Replace(base, "HCLOUD_TOKEN='old-token-value-1'", `HCLOUD_TOKEN='new-secret-value-it'\''s-2'`, 1)
	if string(got) != want {
		t.Errorf("file:\n%s\nwant:\n%s", got, want)
	}
	if st, _ := os.Stat(filepath.Join(dir, ".env.local")); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Errorf("a temporary file stayed: %v", ents)
	}
	// no value in anything the preview or the result say
	for _, s := range append([]string{p.Backup, p.Etag}, p.Notes...) {
		if strings.Contains(s, value) || strings.Contains(s, "old-token-value-1") {
			t.Errorf("a value in %q", s)
		}
	}
	for _, f := range p.Files {
		if strings.Contains(f.Text, "secret") || !strings.Contains(f.Text, "~ HCLOUD_TOKEN: replaced") {
			t.Errorf("preview text %q", f.Text)
		}
	}
	// the copy: the file under its name, the credential empty, a manifest without values, and only the owner reads it
	zr, err := zip.OpenReader(filepath.Join(home, ".local", "state", "viper", "backups", p.Backup))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	files := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		files[f.Name] = string(b)
	}
	if files[".env.local"] != strings.Replace(base, "HCLOUD_TOKEN='old-token-value-1'", "HCLOUD_TOKEN=''", 1) {
		t.Errorf("saved file: %q", files[".env.local"])
	}
	if m := files["manifest.yaml"]; !strings.Contains(m, "action: credential") || !strings.Contains(m, "path: .env.local") || !strings.Contains(m, "HCLOUD_TOKEN") {
		t.Errorf("manifest: %q", m)
	}
	for name, text := range files {
		for _, v := range []string{value, "old-token-value-1", "secret"} {
			if strings.Contains(text, v) {
				t.Errorf("%s holds %q", name, v)
			}
		}
	}
	if st, _ := os.Stat(filepath.Join(home, ".local", "state", "viper", "backups", p.Backup)); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("backup mode %v", st.Mode())
	}
}

func TestACredentialIsAddedAndRemoved(t *testing.T) {
	env, _ := credEnv(t)
	dir := setupDir(t, "A=1\nPROVIDER=hetzner", "")
	if _, err := setCred(t, dir, env, credChange(map[string]string{"VIPER_GH_TOKEN": "ghp_abcdefgh12345"})); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".env.local"))
	if string(b) != "A=1\nPROVIDER=hetzner\nVIPER_GH_TOKEN='ghp_abcdefgh12345'\n" {
		t.Errorf("%q", b)
	}
	if _, err := setCred(t, dir, env, credChange(nil, "VIPER_GH_TOKEN")); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(dir, ".env.local"))
	if string(b) != "A=1\nPROVIDER=hetzner\n" {
		t.Errorf("%q", b)
	}
	p, _ := settingsfile.Files{}.Preview(dir, credChange(nil, "VIPER_GH_TOKEN"), env)
	mustProblem(t, p, "not set in this file")
}

func TestACredentialChangeIsRefusedWhenItCannotBeMadeSafely(t *testing.T) {
	env, _ := credEnv(t)
	long := strings.Repeat("x", 9000)
	for name, c := range map[string]struct {
		text string
		ch   settings.Change
		want string
	}{
		"not a credential":  {base, credChange(map[string]string{"SERVER_TYPE": "abcdefgh12"}), "not a credential"},
		"short":             {base, credChange(map[string]string{"HCLOUD_TOKEN": "short"}), "at least 8"},
		"long":              {base, credChange(map[string]string{"HCLOUD_TOKEN": long}), "at most"},
		"line break":        {base, credChange(map[string]string{"HCLOUD_TOKEN": "abcdefgh\nB=1"}), "no line break"},
		"nothing":           {base, credChange(nil), "nothing to change"},
		"set and removed":   {base, credChange(map[string]string{"HCLOUD_TOKEN": "abcdefgh12"}, "HCLOUD_TOKEN"), "at once"},
		"twice in the file": {base + "HCLOUD_TOKEN=x\n", credChange(map[string]string{"HCLOUD_TOKEN": "abcdefgh12"}), "2 times"},
		"windows line ends": {"A=1\r\n", credChange(map[string]string{"HCLOUD_TOKEN": "abcdefgh12"}), "carriage return"},
		"not readable":      {"A=$(x)\n", credChange(map[string]string{"HCLOUD_TOKEN": "abcdefgh12"}), "unsupported"},
		"unknown target":    {base, settings.Change{Kind: "credential", Target: "elsewhere", Fields: map[string]string{"HCLOUD_TOKEN": "abcdefgh12"}}, "unknown file"},
		"bad name":          {base, settings.Change{Kind: "credential", Target: "machine", Name: "../x", Fields: map[string]string{"HCLOUD_TOKEN": "abcdefgh12"}}, "lowercase"},
		"no such file":      {base, settings.Change{Kind: "credential", Target: "machine", Name: "nope", Fields: map[string]string{"HCLOUD_TOKEN": "abcdefgh12"}}, "make it first"},
	} {
		dir := setupDir(t, c.text, "")
		p, _ := settingsfile.Files{}.Preview(dir, c.ch, env)
		if !strings.Contains(strings.Join(p.Problems, "|"), c.want) {
			t.Errorf("%s: %q", name, p.Problems)
		}
		if _, err := (settingsfile.Files{}).Create(dir, c.ch, env); err == nil {
			t.Errorf("%s: created", name)
		}
		if b, _ := os.ReadFile(filepath.Join(dir, ".env.local")); string(b) != c.text {
			t.Errorf("%s: the file changed: %q", name, b)
		}
	}
}

func TestAChangedFileIsNotReplacedAndALinkIsNotFollowed(t *testing.T) {
	env, home := credEnv(t)
	dir := setupDir(t, base, "")
	ch := credChange(map[string]string{"HCLOUD_TOKEN": "abcdefgh12345"})
	p, _ := settingsfile.Files{}.Preview(dir, ch, env)
	_ = os.WriteFile(filepath.Join(dir, ".env.local"), []byte(base+"# changed at the terminal\n"), 0o600)
	ch.Etag = p.Etag
	if _, err := (settingsfile.Files{}).Create(dir, ch, env); err == nil {
		t.Error("a file that changed was replaced")
	}
	ch.Etag = ""
	if _, err := (settingsfile.Files{}).Create(dir, ch, env); err == nil {
		t.Error("created without a preview")
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "state", "viper", "backups")); err == nil {
		t.Error("a backup of a change that was refused")
	}
	if runtime.GOOS == "windows" {
		return
	}
	other := filepath.Join(t.TempDir(), "dotfile")
	_ = os.WriteFile(other, []byte(base), 0o600)
	dir2 := t.TempDir()
	_ = os.Symlink(other, filepath.Join(dir2, ".env.local"))
	p, _ = settingsfile.Files{}.Preview(dir2, credChange(map[string]string{"HCLOUD_TOKEN": "abcdefgh12345"}), env)
	mustProblem(t, p, "link")
}

func TestTheRightsOfAnExistingFileStay(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no rights there")
	}
	env, _ := credEnv(t)
	dir := setupDir(t, base, "")
	_ = os.Chmod(filepath.Join(dir, ".env.local"), 0o640)
	if _, err := setCred(t, dir, env, credChange(map[string]string{"HCLOUD_TOKEN": "abcdefgh12345"})); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(filepath.Join(dir, ".env.local")); st.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", st.Mode())
	}
}

func TestOnlyFiveBackupsOfAFileStay(t *testing.T) {
	env, home := credEnv(t)
	dir := setupDir(t, base, "")
	for i := 0; i < 8; i++ {
		if _, err := setCred(t, dir, env, credChange(map[string]string{"HCLOUD_TOKEN": "abcdefgh1234" + string(rune('a'+i))})); err != nil {
			t.Fatal(err)
		}
	}
	ents, _ := os.ReadDir(filepath.Join(home, ".local", "state", "viper", "backups"))
	if len(ents) != 5 {
		t.Errorf("%d backups", len(ents))
	}
}

func TestACredentialWrittenIsWhatBashReads(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no bash")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	env, _ := credEnv(t)
	for _, v := range []string{"abcdefgh$HOME", "it's-a-token-1", "`x` \\ \"q\" #c", "ünï-cödé-123", "a b c d e f g h"} {
		dir := setupDir(t, base, "")
		if _, err := setCred(t, dir, env, credChange(map[string]string{"HCLOUD_TOKEN": v})); err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		out, err := exec.Command(bash, "-c", `source "$1"; printf %s "$HCLOUD_TOKEN"`, "_", filepath.Join(dir, ".env.local")).Output()
		if err != nil || string(out) != v {
			t.Errorf("%q: bash reads %q (%v)", v, out, err)
		}
		vars, err := settingsfile.LoadFile(dir, env)
		if err != nil || vars["HCLOUD_TOKEN"] != v {
			t.Errorf("%q: viper reads %q (%v)", v, vars["HCLOUD_TOKEN"], err)
		}
	}
}
