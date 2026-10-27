package model_test

import (
	"os/exec"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/model"
)

func TestWorkDir(t *testing.T) {
	c := &model.Machine{WorkspaceDir: "/Development"}
	for url, want := range map[string]string{
		"": "/Development", "https://git.example.com/o/r.git": "/Development/r", "https://git.example.com/o/r": "/Development/r",
		"git@github.com:o/my-repo.git": "/Development/my-repo", "https://git.example.com/o/r.git/": "/Development/r.git",
		"/srv/git/local.git": "/Development/local",
	} {
		if got := c.WorkDir(url); got != want {
			t.Errorf("%q: got %q, want %q", url, got, want)
		}
	}
}

// Quote must give the shell back exactly one word with the same content.
func TestQuoteSurvivesTheShell(t *testing.T) {
	for _, s := range []string{"", "plain", "with space", "it's", `a"b`, "$HOME `id` $(id)", "a;b|c&d", "line\nbreak", "*", "--flag", "\\n", "ü€"} {
		out, err := exec.Command("bash", "-c", `printf '%s\0' `+model.Quote(s)).Output()
		if err != nil || string(out) != s+"\x00" {
			t.Errorf("%q: got %q (%v)", s, out, err)
		}
	}
	if got := model.QuoteAll("a b", "c"); got != `'a b' 'c'` {
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
		if got := model.SSHFailureKind(stderr); got != want {
			t.Errorf("%q: got %s, want %s", stderr, got, want)
		}
	}
}
