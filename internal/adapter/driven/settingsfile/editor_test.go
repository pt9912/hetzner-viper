package settingsfile_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/settingsfile"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

func editEnv(t *testing.T) settings.Env {
	t.Helper()
	return settings.Env{Lookup: envOf(nil), Home: t.TempDir(), Version: "v0.0.0"}
}

func machineChange(name string, f map[string]string) settings.Change {
	return settings.Change{Kind: "machine", Name: name, Fields: f}
}

func mustProblem(t *testing.T, p settings.Preview, want string) {
	t.Helper()
	if !strings.Contains(strings.Join(p.Problems, "\n"), want) {
		t.Errorf("problems %q lack %q", p.Problems, want)
	}
}

func TestPreviewWritesNothingAndShowsTheFile(t *testing.T) {
	dir := setupDir(t, "SERVER_PREFIX=claude\nDNS_PROVIDER=none\n", "")
	p, err := settingsfile.Files{}.Preview(dir, machineChange("web", map[string]string{"PROVIDER": "hetzner", "SERVER_TYPE": "cpx22"}), editEnv(t))
	if err != nil || len(p.Problems) > 0 || len(p.Broken) > 0 {
		t.Fatalf("%v %+v", err, p)
	}
	if len(p.Files) != 1 || p.Files[0].Path != "machines/web.env.local" || !strings.Contains(p.Files[0].Text, "SERVER_TYPE='cpx22'\n") {
		t.Errorf("%+v", p.Files)
	}
	if _, err := os.Stat(filepath.Join(dir, "machines")); err == nil {
		t.Error("the preview made a directory")
	}
	if len(p.Notes) == 0 || !strings.Contains(p.Notes[0], "HCLOUD_TOKEN") {
		t.Errorf("what viper up still asks for is not named: %q", p.Notes)
	}
}

func TestCreateMakesTheFileThatLoads(t *testing.T) {
	dir := setupDir(t, "SERVER_PREFIX=claude\nDNS_PROVIDER=none\n", "")
	ch := machineChange("box", map[string]string{"PROVIDER": "compose", "SERVER_LOCATION": "default"})
	if _, err := (settingsfile.Files{}).Create(dir, ch, editEnv(t)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "machines", "box.env.local")
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "DNS_PROVIDER='none'") {
		t.Errorf("a container needs no DNS name, the file says: %s", b)
	}
	if ents, _ := os.ReadDir(filepath.Join(dir, "machines")); len(ents) != 1 {
		t.Errorf("a temporary file stayed: %v", ents)
	}
	m, err := settingsfile.Load(dir, "box", editEnv(t), false)
	if err != nil || m.Provider != "compose" {
		t.Fatalf("%v %v", m, err)
	}
	if _, err := (settingsfile.Files{}).Create(dir, ch, editEnv(t)); err == nil {
		t.Error("a second create replaced the file")
	}
}

func TestNamesAndValuesAreRefused(t *testing.T) {
	dir := setupDir(t, "SERVER_PREFIX=claude\n", "")
	for name, want := range map[string]string{
		"Web": "lowercase", "../x": "lowercase", "": "lowercase", "con": "reserved", "com1": "reserved", "example-a": "reserved",
		"viper-admin": "reserved", strings.Repeat("a", 32): "31",
	} {
		p, _ := settingsfile.Files{}.Preview(dir, machineChange(name, nil), editEnv(t))
		mustProblem(t, p, want)
	}
	for _, f := range []map[string]string{
		{"SERVER_TYPE": "a\nB=1"}, {"SERVER_TYPE": "a\x00"}, {"SERVER_TYPE": "a\rb"}, {"SERVER_TYPE": "a\tb"}, {"SERVER_TYPE": strings.Repeat("x", 300)},
		{"SERVER_TYPE": "\xff"}, {"PROVIDER": "evil"}, {"HCLOUD_TOKEN": "secret-token-value"}, {"VIPER_POST_CLONE": "rm -rf /"},
	} {
		p, _ := settingsfile.Files{}.Preview(dir, machineChange("web", f), editEnv(t))
		if len(p.Problems) == 0 {
			t.Errorf("%q was accepted", f)
		}
	}
}

func TestNothingIsReplacedNotEvenInOtherCase(t *testing.T) {
	dir := setupDir(t, "SERVER_PREFIX=claude\n", "PROVIDER=hetzner\n")
	p, _ := settingsfile.Files{}.Preview(dir, machineChange("web", nil), editEnv(t))
	mustProblem(t, p, "exists already")
	other := filepath.Join(dir, "machines", "Dev.env.local")
	_ = os.WriteFile(other, nil, 0o600)
	p, _ = settingsfile.Files{}.Preview(dir, machineChange("dev", nil), editEnv(t))
	mustProblem(t, p, "exists already")
}

func TestALinkedDirectoryIsNotWrittenThrough(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links need rights there")
	}
	dir := setupDir(t, "SERVER_PREFIX=claude\n", "")
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(dir, "machines")); err != nil {
		t.Fatal(err)
	}
	p, _ := settingsfile.Files{}.Preview(dir, machineChange("web", nil), editEnv(t))
	mustProblem(t, p, "link")
	if _, err := (settingsfile.Files{}).Create(dir, machineChange("web", nil), editEnv(t)); err == nil {
		t.Error("written through a link")
	}
	if ents, _ := os.ReadDir(elsewhere); len(ents) != 0 {
		t.Errorf("%v", ents)
	}
}

func TestAMachineNeedsTheBaseFile(t *testing.T) {
	dir := t.TempDir()
	p, _ := settingsfile.Files{}.Preview(dir, machineChange("web", map[string]string{"PROVIDER": "compose"}), editEnv(t))
	mustProblem(t, p, ".env.local")
	p, _ = settingsfile.Files{}.Preview(dir, settings.Change{Kind: "base", Fields: map[string]string{"DNS_PROVIDER": "none"}}, editEnv(t))
	if len(p.Problems) > 0 || len(p.Files) != 1 || p.Files[0].Path != ".env.local" {
		t.Fatalf("%+v", p)
	}
	if _, err := (settingsfile.Files{}).Create(dir, settings.Change{Kind: "base", Fields: map[string]string{"DNS_PROVIDER": "none"}}, editEnv(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := settingsfile.LoadFile(dir, editEnv(t)); err != nil {
		t.Fatal(err)
	}
}

func TestAGroupNeedsItsMachinesAndCanBreakNone(t *testing.T) {
	dir := setupDir(t, "SERVER_PREFIX=claude\nDNS_PROVIDER=none\n", "PROVIDER=compose\nSERVER_LOCATION=default\n")
	writeIn(t, dir, "machines/db.env.local", "PROVIDER=compose\nSERVER_LOCATION=default\n")
	p, _ := settingsfile.Files{}.Preview(dir, settings.Change{Kind: "group", Name: "dev", Members: []string{"web", "nope"}}, editEnv(t))
	mustProblem(t, p, "no machine nope")
	p, _ = settingsfile.Files{}.Preview(dir, settings.Change{Kind: "group", Name: "dev", Members: nil}, editEnv(t))
	mustProblem(t, p, "at least one")
	if _, err := (settingsfile.Files{}).Create(dir, settings.Change{Kind: "group", Name: "dev", Members: []string{"web", "db"}, Fields: map[string]string{"NATS_ENABLED": "true"}}, editEnv(t)); err != nil {
		t.Fatal(err)
	}
	g, err := settingsfile.LoadGroup(dir, "dev", editEnv(t))
	if err != nil || len(g.Machines) != 2 {
		t.Fatalf("%v %v", g, err)
	}
	// a second group that names a machine of the first makes it ambiguous: it is said, and nothing is written
	p, err = settingsfile.Files{}.Create(dir, settings.Change{Kind: "group", Name: "ops", Members: []string{"web"}}, editEnv(t))
	if err == nil || len(p.Broken) != 1 || !strings.HasPrefix(p.Broken[0], "machine web:") || !strings.Contains(p.Broken[0], "--in-group") {
		t.Fatalf("%v %+v", err, p)
	}
	if _, err := os.Stat(filepath.Join(dir, "groups", "ops.env")); err == nil {
		t.Error("the refused group was written")
	}
}

func TestWhatIsWrittenIsWhatBashReads(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no bash")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	for _, v := range []string{"plain", "it's", "ü€", "$HOME `x` \\ \"q\"", "a b #c", "'", "x\\"} {
		dir := setupDir(t, "SERVER_PREFIX=claude\nDNS_PROVIDER=none\n", "")
		if _, err := (settingsfile.Files{}).Create(dir, machineChange("web", map[string]string{"PROVIDER": "hetzner", "SERVER_TYPE": v}), editEnv(t)); err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		f := filepath.Join(dir, "machines", "web.env.local")
		out, err := exec.Command(bash, "-c", `source "$1"; printf %s "$SERVER_TYPE"`, "_", f).Output()
		m, lerr := settingsfile.Load(dir, "web", editEnv(t), false)
		if err != nil || string(out) != v {
			t.Errorf("%q: bash reads %q (%v)", v, out, err)
		}
		if lerr == nil && m.ServerType != v {
			t.Errorf("%q: viper reads %q", v, m.ServerType)
		}
	}
}

func TestANewFailureOfAMachineThatFailedBeforeIsBroken(t *testing.T) {
	dir := setupDir(t, "SERVER_PREFIX=c\n", "PROVIDER=hetzner\n") // no DNS_DOMAIN: web fails already
	if _, err := (settingsfile.Files{}).Create(dir, settings.Change{Kind: "group", Name: "g1", Members: []string{"web"}}, editEnv(t)); err != nil {
		t.Fatal(err)
	}
	p, err := settingsfile.Files{}.Create(dir, settings.Change{Kind: "group", Name: "g2", Members: []string{"web"}}, editEnv(t))
	if err == nil || len(p.Broken) == 0 {
		t.Fatalf("%v %+v", err, p)
	}
}

func TestAGroupMayNameALinkedMachineAndOnlyRealNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links need rights there")
	}
	dir := setupDir(t, "SERVER_PREFIX=claude\nDNS_PROVIDER=none\n", "")
	target := filepath.Join(t.TempDir(), "real")
	_ = os.WriteFile(target, []byte("PROVIDER=hetzner\n"), 0o600)
	_ = os.MkdirAll(filepath.Join(dir, "machines"), 0o700)
	if err := os.Symlink(target, filepath.Join(dir, "machines", "web.env.local")); err != nil {
		t.Fatal(err)
	}
	p, _ := settingsfile.Files{}.Preview(dir, settings.Change{Kind: "group", Name: "dev", Members: []string{"web"}}, editEnv(t))
	if len(p.Problems) > 0 {
		t.Errorf("%v", p.Problems)
	}
	p, _ = settingsfile.Files{}.Preview(dir, settings.Change{Kind: "group", Name: "dev", Members: []string{"../x"}}, editEnv(t))
	mustProblem(t, p, "not a machine name")
}
