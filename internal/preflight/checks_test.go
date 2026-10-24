package preflight_test

import (
	"context"
	"errors"
	"github.com/pt9912/hetzner-viper/internal/preflight"
	"net/http"
	"strings"
	"testing"
)

// github answers like GitHub for user "tester". scopes is the X-OAuth-Scopes header.
func github(scopes string, readonly, norepo, bad bool) roundTrip {
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Host == "api.github.com" && r.URL.Path == "/user":
			if bad {
				return reply(401, `{"message":"Bad credentials"}`, nil), nil
			}
			h := map[string]string{}
			if scopes != "" {
				h["X-OAuth-Scopes"] = scopes
			}
			return reply(200, `{"login":"tester"}`, h), nil
		case r.URL.Host == "api.github.com" && strings.HasPrefix(r.URL.Path, "/repos/"):
			if norepo {
				return reply(404, `{}`, nil), nil
			}
			return reply(200, `{}`, nil), nil
		case r.URL.Host == "github.com" && strings.HasSuffix(r.URL.Path, ".git/info/refs"):
			if readonly {
				return reply(403, ``, nil), nil
			}
			return reply(200, ``, nil), nil
		}
		return reply(404, ``, nil), nil
	}
}

func TestGithubTokenChecks(t *testing.T) {
	e := fixture(t, "VIPER_GH_TOKEN=ghp_secret\n")
	e.http = github("repo, read:org", false, false, false)
	out, _, n := e.run("web", "https://github.com/o/r.git")
	mustContain(t, out, "ok    VIPER_GH_TOKEN valid for tester on github.com", "ok    token can access o/r", "ok    token can push to o/r")
	if n != 0 {
		t.Errorf("%d", n)
	}
	// the token is a header, never in an URL or the output
	for _, r := range e.reqs {
		if strings.Contains(r.URL.String(), "ghp_secret") {
			t.Errorf("token in URL %s", r.URL)
		}
	}
	mustNotContain(t, out, "ghp_secret")
	var push *http.Request
	for _, r := range e.reqs {
		if strings.HasSuffix(r.URL.Path, "/info/refs") {
			push = r
		}
	}
	if push == nil || push.URL.RawQuery != "service=git-receive-pack" || !strings.HasPrefix(push.Header.Get("Authorization"), "Basic ") {
		t.Errorf("push check: %+v", push)
	}

	e.http = github("repo, read:org", true, false, false)
	out, _, n = e.run("web", "https://github.com/o/r.git")
	mustContain(t, out, "FAIL  token cannot push to o/r (fine-grained token: Contents must be Read and write)")
	if n != 1 {
		t.Errorf("%d", n)
	}
	e.http = github("repo, workflow", false, false, false)
	out, _, _ = e.run("web", "")
	mustContain(t, out, "FAIL  classic token lacks scope read:org (or write:org/admin:org) (has: repo, workflow); gh auth login will reject it")
	mustNotContain(t, out, "lacks scope repo")
	e.http = github("read:org", false, false, false)
	out, _, _ = e.run("web", "")
	mustContain(t, out, "FAIL  classic token lacks scope repo (has: read:org)")
	e.http = github("", false, true, false) // a fine-grained token sends no scopes
	out, _, _ = e.run("web", "https://github.com/o/r.git")
	mustContain(t, out, "FAIL  token cannot access o/r (fine-grained token without this repository?)")
	mustNotContain(t, out, "lacks scope")
	e.http = github("", false, false, true)
	out, _, _ = e.run("web", "")
	mustContain(t, out, "FAIL  VIPER_GH_TOKEN rejected by github.com: HTTP 401")
	mustNotContain(t, out, "Bad credentials", "ghp_secret")
}

func TestGithubChecksOnlyForRepositoriesOfTheHost(t *testing.T) {
	e := fixture(t, "VIPER_GH_TOKEN=ghp_x\nVIPER_GH_HOST=ghe.example.com\n")
	e.http = func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Host == "ghe.example.com" && r.URL.Path == "/api/v3/user":
			return reply(200, `{"login":"tester"}`, nil), nil
		case r.URL.Host == "ghe.example.com" && r.URL.Path == "/api/v3/repos/o/r":
			return reply(200, `{}`, nil), nil
		case r.URL.Host == "ghe.example.com" && r.URL.Path == "/o/r.git/info/refs":
			return reply(200, ``, nil), nil
		}
		return reply(404, ``, nil), nil
	}
	out, _, n := e.run("web", "git@ghe.example.com:o/r.git")
	mustContain(t, out, "valid for tester on ghe.example.com", "token can push to o/r", "ok    SSH repository git@ghe.example.com:o/r.git will be cloned over HTTPS with VIPER_GH_TOKEN")
	if n != 0 {
		t.Errorf("%d: %s", n, out)
	}
	// a deploy key wins over the token for an SSH URL: no API check of the repository
	e.machine("web", "GIT_DEPLOY_KEY_FILE="+e.home+"/.ssh/hetzner-build\n")
	e.reqs = nil
	out, _, _ = e.run("web", "git@ghe.example.com:o/r.git")
	mustNotContain(t, out, "token can access")
	mustContain(t, out, "info  SSH repository git@ghe.example.com:o/r.git with deploy key")
	// another host's repository is not checked with this token
	e.machine("web", "")
	out, _, _ = e.run("web", "https://github.com/o/r.git")
	mustNotContain(t, out, "token can access")
}

func TestGitIdentity(t *testing.T) {
	for _, c := range []struct{ settings, want string }{
		{"VIPER_GIT_USER_NAME=Ada\nVIPER_GIT_USER_EMAIL=ada@example.com\n", "ok    git commits on the server as Ada <ada@example.com>"},
		{"VIPER_GIT_USER_EMAIL=ada@example.com\nVIPER_GH_TOKEN=x\n", "info  git identity: e-mail ada@example.com, the rest from the GitHub account (name, noreply address on github.com)"},
		{"VIPER_GIT_USER_NAME=Ada\nVIPER_GH_TOKEN=x\n", "info  git identity: name Ada, the rest from the GitHub account"},
		{"VIPER_GH_TOKEN=x\n", "info  git identity: the rest from the GitHub account"},
		{"VIPER_GIT_USER_EMAIL=not-an-address\n", "FAIL  VIPER_GIT_USER_EMAIL is not an e-mail address: not-an-address"},
		{"VIPER_GIT_USER_EMAIL=\"a b@example.com\"\n", "FAIL  VIPER_GIT_USER_EMAIL is not an e-mail address"},
		{"VIPER_GIT_USER_NAME=\"Ada\nLovelace\"\n", "FAIL  VIPER_GIT_USER_NAME / VIPER_GIT_USER_EMAIL: no line breaks or control characters"},
	} {
		e := fixture(t, c.settings)
		e.http = github("repo, read:org", false, false, false)
		out, _, _ := e.run("web", "")
		mustContain(t, out, c.want)
	}
}

func gateway(code int, body string) roundTrip {
	return func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/messages" || r.Method != "POST" || r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("Authorization") != "Bearer gw-token" {
			return reply(404, `{"error":{"message":"wrong request"}}`, nil), nil
		}
		return reply(code, body, nil), nil
	}
}

func TestGatewayIsCheckedWithARealRequest(t *testing.T) {
	base := "VIPER_ANTHROPIC_BASE_URL=https://gw.example.com/\nVIPER_ANTHROPIC_AUTH_TOKEN=gw-token\nVIPER_CLAUDE_MODEL=\"glm-4.6[1m]\"\n"
	e := fixture(t, base)
	e.http = gateway(200, `{}`)
	out, _, n := e.run("web", "")
	mustContain(t, out, "ok    https://gw.example.com answers with model glm-4.6 (Anthropic Messages API)")
	if n != 0 {
		t.Errorf("%d: %s", n, out)
	}
	var sent string
	for _, r := range e.reqs {
		if r.URL.Path == "/v1/messages" {
			b := new(strings.Builder)
			if r.Body != nil {
				buf := make([]byte, 1024)
				k, _ := r.Body.Read(buf)
				b.Write(buf[:k])
			}
			sent = b.String()
		}
	}
	_ = sent
	for _, c := range []struct {
		code int
		body string
		want string
	}{
		{401, `{"error":{"message":"invalid api key"}}`, "FAIL  https://gw.example.com rejects VIPER_ANTHROPIC_AUTH_TOKEN (HTTP 401: invalid api key)"},
		{403, `{"error":"forbidden"}`, "FAIL  https://gw.example.com rejects VIPER_ANTHROPIC_AUTH_TOKEN (HTTP 403: forbidden)"},
		{404, `{"message":"model not found"}`, "FAIL  https://gw.example.com: model glm-4.6 or the Messages API not found (HTTP 404: model not found)"},
		{500, `oops`, "FAIL  https://gw.example.com: HTTP 500"},
	} {
		e.http = gateway(c.code, c.body)
		out, _, _ := e.run("web", "")
		mustContain(t, out, c.want)
	}
	e.http = func(*http.Request) (*http.Response, error) { return nil, errors.New("dial tcp: connection refused") }
	out, _, _ = e.run("web", "")
	mustContain(t, out, "FAIL  https://gw.example.com not reachable: ")
	mustNotContain(t, out, "gw-token")
}

func TestGatewaySettingsAreChecked(t *testing.T) {
	for _, c := range []struct {
		settings string
		want     []string
	}{
		{"VIPER_ANTHROPIC_BASE_URL=https://gw.example.com\nVIPER_CLAUDE_TOKEN=t\nVIPER_ANTHROPIC_AUTH_TOKEN=a\nVIPER_CLAUDE_MODEL=m\n",
			[]string{"FAIL  set either VIPER_CLAUDE_TOKEN or VIPER_ANTHROPIC_BASE_URL, not both"}},
		{"VIPER_ANTHROPIC_BASE_URL=https://gw.example.com\n",
			[]string{"FAIL  VIPER_ANTHROPIC_AUTH_TOKEN is required with VIPER_ANTHROPIC_BASE_URL", "FAIL  VIPER_CLAUDE_MODEL is required with VIPER_ANTHROPIC_BASE_URL"}},
		{"VIPER_ANTHROPIC_BASE_URL=https://gw.example.com\nVIPER_ANTHROPIC_AUTH_TOKEN=a\nVIPER_CLAUDE_MODEL=m\nVIPER_CLAUDE_MAX_CONTEXT_TOKENS=1m\n",
			[]string{"FAIL  VIPER_CLAUDE_MAX_CONTEXT_TOKENS must be a number of tokens"}},
		{"VIPER_ANTHROPIC_BASE_URL=ftp://gw\nVIPER_ANTHROPIC_AUTH_TOKEN=a\nVIPER_CLAUDE_MODEL=m\n",
			[]string{"FAIL  VIPER_ANTHROPIC_BASE_URL must start with https:// (or http://): ftp://gw"}},
		{"VIPER_ANTHROPIC_BASE_URL=http://gw.example.com\nVIPER_ANTHROPIC_AUTH_TOKEN=gw-token\nVIPER_CLAUDE_MODEL=m\n",
			[]string{"WARN  VIPER_ANTHROPIC_BASE_URL is not https: prompts and the token travel unencrypted"}},
		{"VIPER_ANTHROPIC_BASE_URL=https://user:hunter2@gw.example.com\nVIPER_ANTHROPIC_AUTH_TOKEN=gw-token\nVIPER_CLAUDE_MODEL=m\n",
			[]string{"https://***@gw.example.com"}},
		{"VIPER_CLAUDE_TOKEN=\"abc def\"\n", []string{"FAIL  VIPER_CLAUDE_TOKEN contains whitespace (copied with a line break?)"}},
		{"VIPER_CLAUDE_TOKEN=sk-ant-oat01-x\n", []string{"ok    VIPER_CLAUDE_TOKEN set: Claude Code will be logged in on the server"}},
	} {
		e := fixture(t, c.settings)
		e.http = gateway(200, `{}`)
		out, _, _ := e.run("web", "")
		mustContain(t, out, c.want...)
		mustNotContain(t, out, "hunter2", "sk-ant-oat01-x\n")
	}
}

func TestHomebrewSettings(t *testing.T) {
	for _, c := range []struct {
		settings string
		want     []string
		notWant  []string
	}{
		{"VIPER_BREW_TAPS=\"hashicorp/tap\"\nVIPER_BREW_PACKAGES=\"ripgrep fd\n  hashicorp/tap/terraform\"\nVIPER_BREW_TRUST=hashicorp/tap/terraform\n",
			[]string{"info  Homebrew taps: hashicorp/tap; trust: hashicorp/tap/terraform; formulae: ripgrep fd hashicorp/tap/terraform"}, []string{"FAIL"}},
		{"VIPER_BREW_PACKAGES=ripgrep\n", []string{"info  Homebrew taps: none; trust: none; formulae: ripgrep"}, []string{"FAIL"}},
		{"VIPER_HOMEBREW=0\nVIPER_BREW_PACKAGES=ripgrep\n", []string{"FAIL  VIPER_BREW_TAPS / VIPER_BREW_TRUST / VIPER_BREW_PACKAGES need VIPER_HOMEBREW=1"}, []string{"Homebrew will be installed"}},
		{"VIPER_BREW_PACKAGES=\"bad;name\"\n", []string{"FAIL  VIPER_BREW_PACKAGES: not a formula (name or owner/repo/name): bad;name"}, nil},
		{"VIPER_BREW_TRUST=\"nonsense\"\n", []string{"FAIL  VIPER_BREW_TRUST: not a tap (owner/repo) or formula (owner/repo/name), optionally with cask:/command:: nonsense"}, nil},
		{"VIPER_BREW_TRUST=cask:o/t/name\n", []string{"info  Homebrew taps: none; trust: cask:o/t/name"}, []string{"FAIL"}},
		{"VIPER_BREW_TAPS=\"o/t=https://user:tok3n@git.example.com/o/t.git\"\n",
			[]string{"FAIL  VIPER_BREW_TAPS: credentials in the tap URL (o/t=https://***@git.example.com/o/t.git): use a deploy key (ssh URL) or a public tap"}, []string{"tok3n"}},
		{"VIPER_BREW_TAPS=\"o/t=git@git.example.com:o/t.git\"\n", []string{"info  Homebrew taps: o/t=git@git.example.com:o/t.git"}, []string{"FAIL"}},
		{"VIPER_BREW_TAPS=\"not a tap\"\n", []string{"FAIL  VIPER_BREW_TAPS: not a tap (owner/repo or owner/repo=URL): not"}, nil},
		{"VIPER_BREWFILE=1\n", []string{"info  VIPER_BREWFILE=1 without a repository: nothing to apply"}, []string{"FAIL"}},
		{"VIPER_BREWFILE=1\nVIPER_HOMEBREW=0\n", []string{"FAIL  VIPER_BREWFILE=1 needs VIPER_HOMEBREW=1"}, nil},
	} {
		e := fixture(t, c.settings)
		out, _, _ := e.run("web", "")
		mustContain(t, out, c.want...)
		mustNotContain(t, out, c.notWant...)
	}
	e := fixture(t, "VIPER_BREWFILE=1\n")
	e.git = func(context.Context, string) error { return nil }
	out, _, _ := e.run("web", "https://git.example.com/o/r.git")
	mustContain(t, out, "WARN  the repository's Brewfile will be applied: it is Ruby code run by brew bundle")
}

func TestSwapSetting(t *testing.T) {
	for v, want := range map[string]string{
		"1": "ok    swap file of 1 x the server's RAM", "0.5": "ok    swap file of 0.5 x the server's RAM", "4": "ok    swap file of 4 x", ".5": "ok    swap file of .5 x",
		"4G": "ok    swap file of 4G on the server", "512M": "ok    swap file of 512M on the server",
		"5": "FAIL  VIPER_SWAP must be 0", "0.05": "FAIL  VIPER_SWAP must be 0", "0G": "FAIL  VIPER_SWAP must be 0", "lots": "FAIL  VIPER_SWAP must be 0", "4GB": "FAIL  VIPER_SWAP must be 0",
	} {
		e := fixture(t, "VIPER_SWAP="+v+"\n")
		out, _, _ := e.run("web", "")
		mustContain(t, out, want)
	}
	e := fixture(t, "")
	out, _, _ := e.run("web", "")
	mustNotContain(t, out, "swap")
}

func TestCodeServerSettings(t *testing.T) {
	for _, c := range []struct{ settings, want string }{
		{"VIPER_CODE_SERVER=1\n", "FAIL  VIPER_CODE_SERVER=1 needs VIPER_CODE_SERVER_PASSWORD"},
		{"VIPER_CODE_SERVER=1\nVIPER_CODE_SERVER_PASSWORD=short\n", "FAIL  VIPER_CODE_SERVER_PASSWORD: at least 12 characters, no spaces or line breaks"},
		{"VIPER_CODE_SERVER=1\nVIPER_CODE_SERVER_PASSWORD=\"has space 123456\"\n", "FAIL  VIPER_CODE_SERVER_PASSWORD: at least 12 characters"},
		{"VIPER_CODE_SERVER=1\nVIPER_CODE_SERVER_PASSWORD='p@ss$word!x1234'\n", "ok    code-server will be installed (VS Code in the browser, reachable only through an SSH tunnel to 127.0.0.1:8080)"},
		{"VIPER_CODE_SERVER=2\n", "FAIL  VIPER_CODE_SERVER must be 0 or 1: 2"},
		{"VIPER_CODE_SERVER_PASSWORD=whatever-long-pw\n", "info  VIPER_CODE_SERVER_PASSWORD is set but VIPER_CODE_SERVER is not 1: ignored"},
	} {
		e := fixture(t, c.settings)
		out, _, _ := e.run("web", "")
		mustContain(t, out, c.want)
		mustNotContain(t, out, "p@ss$word", "whatever-long-pw", "short\n")
	}
}

func TestRepositoryChecks(t *testing.T) {
	reach := func(context.Context, string) error { return nil }
	unreach := func(context.Context, string) error { return errors.New("exit 128") }
	for _, c := range []struct {
		settings, repo string
		git            func(context.Context, string) error
		want           string
	}{
		{"", "https://git.example.com/o/r.git", reach, "ok    repository https://git.example.com/o/r.git reachable"},
		{"", "/does/not/exist.git", unreach, "FAIL  repository /does/not/exist.git not reachable without credentials (private? set VIPER_GH_TOKEN or GIT_DEPLOY_KEY_FILE)"},
		{"", "https://git.example.com/o/r.git", nil, "info  git not installed locally: repository not checked"},
		{"", "--upload-pack=evil", reach, "FAIL  REPO_URL must not start with '-': --upload-pack=evil"},
		{"", "https://user:tok3n@git.example.com/o/r.git", reach, "FAIL  REPO_URL contains credentials (https://***@git.example.com/o/r.git): use VIPER_GH_TOKEN or GIT_DEPLOY_KEY_FILE instead"},
		{"", "ssh://git@git.example.com/o/r.git", reach, "FAIL  SSH repository ssh://***@git.example.com/o/r.git needs GIT_DEPLOY_KEY_FILE"},
		{"", "git@github.com:o/r.git", reach, "FAIL  SSH repository git@github.com:o/r.git needs GIT_DEPLOY_KEY_FILE (the server has no SSH key for the git host); or use an HTTPS URL, or VIPER_GH_TOKEN for github.com"},
		{"REPO_URL=https://git.example.com/from-settings.git\n", "", reach, "ok    repository https://git.example.com/from-settings.git reachable"},
	} {
		e := fixture(t, c.settings)
		e.git = c.git
		out, _, _ := e.run("web", c.repo)
		mustContain(t, out, c.want)
		mustNotContain(t, out, "tok3n")
	}
	// the repository the server was started with is remembered
	e := fixture(t, "")
	e.git = reach
	c := e.config("web")
	if err := writeFile(c.RepoState, "https://git.example.com/remembered.git\n"); err != nil {
		t.Fatal(err)
	}
	out, _, _ := e.run("web", "")
	mustContain(t, out, "ok    repository https://git.example.com/remembered.git reachable")
	out, _, _ = e.run("web", "https://git.example.com/given.git")
	mustContain(t, out, "given.git reachable")
}

func TestRedaction(t *testing.T) {
	if got := preflight.RedactURL("https://user:tok@host/x"); got != "https://***@host/x" {
		t.Error(got)
	}
	if got := preflight.RedactURL("git@host:o/r.git"); got != "git@host:o/r.git" {
		t.Error(got)
	}
	if got := preflight.RedactTaps([]string{"a/b", "c/d=https://u:t@h/x"}); got != "a/b c/d=https://***@h/x" {
		t.Error(got)
	}
}
