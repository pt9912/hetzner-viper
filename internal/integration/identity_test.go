package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func (e *Env) withGitHubToken() {
	e.Tokens["VIPER_GH_TOKEN"] = "ghp_x"
}

func TestGitIdentityFromTheGithubAccount(t *testing.T) {
	e := New(t, "")
	e.withGitHubToken()
	r := e.MustUp("web", "")
	if e.GitConfig("user.name") != "Test User" || e.GitConfig("user.email") != "4242+tester@users.noreply.github.com" {
		t.Errorf("identity %q <%q>", e.GitConfig("user.name"), e.GitConfig("user.email"))
	}
	r.Has(t, "git: commits as Test User <4242+tester@users.noreply.github.com>\n")
}

func TestGitIdentityLoginWhenTheAccountHasNoName(t *testing.T) {
	e := New(t, "")
	e.withGitHubToken()
	e.Srv.Env["MOCK_GH_NONAME"] = "1"
	e.MustUp("web", "")
	if e.GitConfig("user.name") != "tester" {
		t.Errorf("name %q", e.GitConfig("user.name"))
	}
}

func TestExplicitGitIdentityWins(t *testing.T) {
	e := New(t, "")
	e.withGitHubToken()
	e.Machine("web", "VIPER_GIT_USER_NAME=\"Ada Lovelace\"\nVIPER_GIT_USER_EMAIL=ada@example.invalid\n")
	e.MustUp("web", "")
	if e.GitConfig("user.name") != "Ada Lovelace" || e.GitConfig("user.email") != "ada@example.invalid" {
		t.Errorf("identity %q <%q>", e.GitConfig("user.name"), e.GitConfig("user.email"))
	}
	for _, c := range e.Srv.Calls() {
		if strings.Contains(c, "gh api") {
			t.Errorf("no lookup needed, but: %s", c)
		}
	}
}

func TestOnlyTheMissingPartComesFromGithub(t *testing.T) {
	e := New(t, "")
	e.withGitHubToken()
	e.Machine("web", "VIPER_GIT_USER_NAME=\"Ada Lovelace\"\n")
	e.MustUp("web", "")
	if e.GitConfig("user.name") != "Ada Lovelace" || e.GitConfig("user.email") != "4242+tester@users.noreply.github.com" {
		t.Errorf("identity %q <%q>", e.GitConfig("user.name"), e.GitConfig("user.email"))
	}
}

func TestDerivedIdentityDoesNotOverwriteAValueAlreadyOnTheServer(t *testing.T) {
	e := New(t, "")
	e.withGitHubToken()
	os.MkdirAll(e.Srv.Home, 0o755)
	if out, err := gitCmd("", "config", "--file", filepath.Join(e.Srv.Home, ".gitconfig"), "user.name", "Set By Hand"); err != nil {
		t.Fatal(out)
	}
	e.MustUp("web", "")
	if e.GitConfig("user.name") != "Set By Hand" || e.GitConfig("user.email") != "4242+tester@users.noreply.github.com" {
		t.Errorf("identity %q <%q>", e.GitConfig("user.name"), e.GitConfig("user.email"))
	}
}

func TestNoIdentityWithoutSettingsAndAWarning(t *testing.T) {
	e := New(t, "")
	e.Preflight("web", "").Has(t, "info  no VIPER_GIT_USER_NAME / VIPER_GIT_USER_EMAIL: git commits on the server fail")
	r := e.MustUp("web", "")
	r.Has(t, "git user.name/user.email are not set on the server")
	if e.GitConfig("user.name") != "" {
		t.Errorf("name %q", e.GitConfig("user.name"))
	}
}

func TestGitIdentityIsChecked(t *testing.T) {
	e := New(t, "")
	e.withGitHubToken()
	e.MustUp("web", "")
	e.Check("web").Has(t, "ok    git commits as Test User <4242+tester@users.noreply.github.com>")
	if out, err := gitCmd("", "config", "--file", filepath.Join(e.Srv.Home, ".gitconfig"), "--unset", "user.email"); err != nil {
		t.Fatal(out)
	}
	e.Check("web").Has(t, "WARN  git user.name/user.email are not set on the server")
}

func TestBadGitIdentityIsRefusedByThePreflight(t *testing.T) {
	e := New(t, "")
	e.Machine("web", "VIPER_GIT_USER_NAME=\"A\"\nVIPER_GIT_USER_EMAIL=\"not an address\"\n")
	r := e.Preflight("web", "")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "VIPER_GIT_USER_EMAIL is not an e-mail address")
}
