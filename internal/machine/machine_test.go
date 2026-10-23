package machine

import (
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
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\nWORKSPACE_DIR=/Development\n"), 0o600)
	c, err := config.Load(dir, "web", config.Env{Home: home, Lookup: func(string) (string, bool) { return "", false }}, false)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSSHConfigPinsTheHostKeyOfThisServer(t *testing.T) {
	c := testConfig(t)
	if err := WriteSSHConfig(c, "203.0.113.7"); err != nil {
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
	os.WriteFile(c.KnownPin, []byte("claude-web.example.com ssh-ed25519 AAAA\n"), 0o600)
	WriteSSHConfig(c, "203.0.113.7")
	b, _ = os.ReadFile(c.SSHConfig)
	if !strings.Contains(string(b), "    UserKnownHostsFile "+c.KnownPin+"\n") {
		t.Errorf("a pinned key is used:\n%s", b)
	}
	// an empty pin file does not count
	os.WriteFile(c.KnownPin, nil, 0o600)
	WriteSSHConfig(c, "203.0.113.7")
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
		if got := WorkDir(c, url); got != want {
			t.Errorf("%q: got %q, want %q", url, got, want)
		}
	}
}

// Quote must give the shell back exactly one word with the same content.
func TestQuoteSurvivesTheShell(t *testing.T) {
	for _, s := range []string{"", "plain", "with space", "it's", `a"b`, "$HOME `id` $(id)", "a;b|c&d", "line\nbreak", "*", "--flag", "\\n", "ü€"} {
		out, err := exec.Command("bash", "-c", `printf '%s\0' `+Quote(s)).Output()
		if err != nil || string(out) != s+"\x00" {
			t.Errorf("%q: got %q (%v)", s, out, err)
		}
	}
	if got := QuoteAll("a b", "c"); got != `'a b' 'c'` {
		t.Error(got)
	}
}
