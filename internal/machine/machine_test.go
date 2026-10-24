package machine_test

import (
	"errors"
	"github.com/pt9912/hetzner-viper/internal/machine"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/config"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	home, dir := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\nWORKSPACE_DIR=/Development\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(dir, "web", config.Env{Home: home, Lookup: func(string) (string, bool) { return "", false }}, false)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSSHConfigPinsTheHostKeyOfThisServer(t *testing.T) {
	c := testConfig(t)
	if err := machine.WriteSSHConfig(c, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(c.SSHConfig)
	for _, want := range []string{
		"Host hetzner-claude-web\n", "    HostName 203.0.113.7\n", "    HostKeyAlias claude-web.example.com\n", "    User builder\n",
		"    IdentityFile " + c.SSHKey + "\n", "    StrictHostKeyChecking yes\n", "    CheckHostIP no\n",
		"    UserKnownHostsFile " + filepath.Join(c.SSHDir, "known_hosts_hetzner_claude_ca") + "\n", // no pin yet: host CA
		"    HostKeyAlgorithms ssh-ed25519-cert-v01@openssh.com,ssh-ed25519\n", "    ForwardAgent no\n", "    ForwardX11 no\n",
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %q in\n%s", want, b)
		}
	}
	if st, _ := os.Stat(c.SSHConfig); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	if err := os.WriteFile(c.KnownPin, []byte("claude-web.example.com ssh-ed25519 AAAA\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := machine.WriteSSHConfig(c, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(c.SSHConfig)
	if !strings.Contains(string(b), "    UserKnownHostsFile "+c.KnownPin+"\n") {
		t.Errorf("a pinned key is used:\n%s", b)
	}
	// an empty pin file does not count
	if err := os.WriteFile(c.KnownPin, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := machine.WriteSSHConfig(c, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(c.SSHConfig)
	if strings.Contains(string(b), "UserKnownHostsFile "+c.KnownPin) {
		t.Errorf("empty pin used:\n%s", b)
	}
}

func TestWorkDir(t *testing.T) {
	c := testConfig(t)
	for url, want := range map[string]string{
		"": "/Development", "https://git.example.com/o/r.git": "/Development/r", "https://git.example.com/o/r": "/Development/r",
		"git@github.com:o/my-repo.git": "/Development/my-repo", "https://git.example.com/o/r.git/": "/Development/r.git",
		"/srv/git/local.git": "/Development/local",
	} {
		if got := machine.WorkDir(c, url); got != want {
			t.Errorf("%q: got %q, want %q", url, got, want)
		}
	}
}

// Quote must give the shell back exactly one word with the same content.
func TestQuoteSurvivesTheShell(t *testing.T) {
	for _, s := range []string{"", "plain", "with space", "it's", `a"b`, "$HOME `id` $(id)", "a;b|c&d", "line\nbreak", "*", "--flag", "\\n", "ü€"} {
		out, err := exec.Command("bash", "-c", `printf '%s\0' `+machine.Quote(s)).Output()
		if err != nil || string(out) != s+"\x00" {
			t.Errorf("%q: got %q (%v)", s, out, err)
		}
	}
	if got := machine.QuoteAll("a b", "c"); got != `'a b' 'c'` {
		t.Error(got)
	}
}

func TestSSHFailureKind(t *testing.T) {
	for stderr, want := range map[string]string{
		"Host key verification failed.": "hostkey", "WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!": "hostkey",
		"Host key for x has changed and you have requested strict checking.": "hostkey", "No ED25519 host key is known for x": "hostkey",
		"Certificate invalid: expired": "hostkey", "builder@x: Permission denied (publickey).": "auth",
		"ssh: connect to host x port 22: Connection timed out": "network", "": "network",
	} {
		if got := machine.SSHFailureKind(stderr); got != want {
			t.Errorf("%q: got %s, want %s", stderr, got, want)
		}
	}
}

func TestForgetStalePin(t *testing.T) {
	c := testConfig(t)
	var out strings.Builder
	if err := os.MkdirAll(c.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.KnownPin, []byte("pin"), 0o600); err != nil {
		t.Fatal(err)
	}
	machine.ForgetStalePin(c, "5", &out) // no recorded ID: keep
	if err := os.WriteFile(c.IDState, []byte("5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	machine.ForgetStalePin(c, "5", &out) // same server: keep
	if _, err := os.Stat(c.KnownPin); err != nil || out.Len() != 0 {
		t.Fatalf("kept: %v %q", err, out.String())
	}
	machine.ForgetStalePin(c, "6", &out)
	if _, err := os.Stat(c.KnownPin); err == nil || out.String() != "Server claude-web was recreated (ID 5 -> 6); dropping its old pinned host key.\n" {
		t.Errorf("dropped: %v %q", err, out.String())
	}
}

func TestPinFromServer(t *testing.T) {
	c := testConfig(t)
	if err := os.MkdirAll(c.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	var asked []string
	ssh := func(args ...string) (string, error) {
		asked = args
		return "ssh-ed25519 AAAAhostkey root@server\n", nil
	}
	machine.PinFromServer(c, "7", ssh, &out)
	b, _ := os.ReadFile(c.KnownPin)
	if string(b) != "claude-web.example.com ssh-ed25519 AAAAhostkey\n" {
		t.Errorf("pin %q", b)
	}
	if id, _ := os.ReadFile(c.IDState); string(id) != "7\n" {
		t.Errorf("id %q", id)
	}
	if out.String() != "Pinned host key of claude-web (verified via host CA).\n" || asked[len(asked)-1] != "cat /etc/ssh/ssh_host_ed25519_key.pub" {
		t.Errorf("%q %q", out.String(), asked)
	}
	// an existing pin stays; garbage is ignored
	asked = nil
	machine.PinFromServer(c, "8", ssh, &out)
	if asked != nil {
		t.Error("asked although pinned")
	}
	_ = os.Remove(c.KnownPin) // may not exist
	machine.PinFromServer(c, "8", func(...string) (string, error) { return "garbage", nil }, &out)
	machine.PinFromServer(c, "8", func(...string) (string, error) { return "", errors.New("x") }, &out)
	if _, err := os.Stat(c.KnownPin); err == nil {
		t.Error("pinned garbage")
	}
}

func TestSSHIncludeEditsInPlaceAndKeepsASymlink(t *testing.T) {
	c := testConfig(t)
	if err := os.MkdirAll(c.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dots := t.TempDir()
	target := filepath.Join(dots, "ssh_config")
	if err := os.WriteFile(target, []byte("Host other\n  User x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(c.SSHDir, "config")); err != nil {
		t.Fatal(err)
	}
	line := "Include " + c.SSHConfig + "\n"
	if err := machine.SSHInclude(c, "add"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); string(b) != line+"Host other\n  User x\n" {
		t.Errorf("added: %q", b)
	}
	if st, _ := os.Lstat(filepath.Join(c.SSHDir, "config")); st.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced")
	}
	if err := machine.SSHInclude(c, "add"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); strings.Count(string(b), "Include /") != 1 {
		t.Errorf("added twice: %q", b)
	}
	if st, _ := os.Stat(target); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	if err := machine.SSHInclude(c, "remove"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); string(b) != "Host other\n  User x\n" {
		t.Errorf("removed: %q", b)
	}
}

func TestSSHIncludeCreatesTheFileAndFailsOnAReadOnlyOne(t *testing.T) {
	c := testConfig(t)
	if err := os.MkdirAll(c.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := machine.SSHInclude(c, "add"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(c.SSHDir, "config")); string(b) != "Include "+c.SSHConfig+"\n" {
		t.Errorf("%q", b)
	}
	if os.Getuid() == 0 {
		return
	}
	if err := os.Chmod(filepath.Join(c.SSHDir, "config"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := machine.SSHInclude(c, "remove"); err == nil {
		t.Error("a read-only file cannot be edited")
	}
	if w := machine.SSHIncludeWarning(c); !strings.HasPrefix(w, "WARNING: could not edit ~/.ssh/config (") || !strings.Contains(w, "add or remove the line 'Include "+c.SSHConfig+"' yourself.") {
		t.Errorf("%q", w)
	}
}

func TestParallelIncludesDoNotLoseLines(t *testing.T) {
	c := testConfig(t)
	if err := os.MkdirAll(c.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.SSHDir, "config"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error)
	for i := 0; i < 8; i++ {
		go func(i int) {
			cc := *c
			cc.SSHConfig = filepath.Join(c.SSHDir, "config_m"+string(rune('a'+i)))
			done <- machine.SSHInclude(&cc, "add")
		}(i)
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(c.SSHDir, "config"))
	if n := strings.Count(string(b), "Include /"); n != 8 {
		t.Errorf("%d lines, want 8:\n%s", n, b)
	}
}
