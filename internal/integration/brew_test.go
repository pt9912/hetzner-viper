package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const brewSettings = `VIPER_BREW_TAPS="hashicorp/tap acme/tools=https://git.example.invalid/tools.git"
VIPER_BREW_TRUST="hashicorp/tap acme/tools/widget cask:acme/tools/app"
VIPER_BREW_PACKAGES="ripgrep hashicorp/tap/terraform"
`

func lineNo(log, prefix string) int {
	for i, l := range strings.Split(log, "\n") {
		if strings.HasPrefix(l, prefix) {
			return i
		}
	}
	return -1
}

func TestBrewTapsTrustAndInstallsInThisOrder(t *testing.T) {
	e := New(t, "")
	e.Machine("web", brewSettings)
	r := e.MustUp("web", "")
	for _, want := range []string{"brew: tap hashicorp/tap\n", "brew: tap acme/tools\n", "brew: trusted tap hashicorp/tap\n", "brew: trusted formula acme/tools/widget\n",
		"brew: trusted cask acme/tools/app\n", "brew: installed ripgrep hashicorp/tap/terraform\n"} {
		r.Has(t, want)
	}
	log := e.Srv.File("brew.log")
	if !strings.Contains(log, "brew tap acme/tools https://git.example.invalid/tools.git\n") {
		t.Errorf("custom tap URL not passed:\n%s", log)
	}
	tap, trust, install := lineNo(log, "brew tap"), lineNo(log, "brew trust"), lineNo(log, "brew install")
	if !(tap >= 0 && tap < trust && trust < install) {
		t.Errorf("order tap %d < trust %d < install %d:\n%s", tap, trust, install, log)
	}
}

func TestBrewfileOfTheRepositoryIsApplied(t *testing.T) {
	tool(t, "git")
	e := New(t, "")
	origin := filepath.Join(t.TempDir(), "origin.git")
	seed := filepath.Join(t.TempDir(), "seed")
	run := func(dir string, args ...string) {
		t.Helper()
		if out, err := gitCmd(dir, args...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("", "init", "-q", "--bare", origin)
	run("", "clone", "-q", origin, seed)
	os.WriteFile(filepath.Join(seed, "Brewfile"), []byte("tap \"acme/tools\", trusted: true\nbrew \"jq\"\nbrew \"acme/tools/widget\"\n"), 0o644)
	run(seed, "add", "Brewfile")
	run(seed, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "brewfile")
	run(seed, "push", "-q", "origin", "HEAD")
	e.Machine("web", "REPO_URL="+origin+"\n")
	e.MustUp("web", "")
	if strings.Contains(e.Srv.File("brew.log"), "brew bundle") {
		t.Error("applied without VIPER_BREWFILE=1")
	}
	e.Machine("web", "REPO_URL="+origin+"\nVIPER_BREWFILE=1\n")
	pre := e.Preflight("web", "")
	pre.Has(t, "WARN  the repository's Brewfile will be applied: it is Ruby code")
	r := e.MustUp("web", "")
	r.Has(t, "brew: "+filepath.Join(e.Config("web").WorkspaceDir, "origin", "Brewfile")+" applied\n")
	if !strings.Contains(e.Srv.File("brew.log"), "brew bundle install --no-upgrade --file=") {
		t.Errorf("bundle:\n%s", e.Srv.File("brew.log"))
	}
	e.Check("web").Has(t, "ok    Homebrew: Brewfile of the repository satisfied")
}

func TestCheckReportsMissingFormulaAndTrust(t *testing.T) {
	e := New(t, "")
	e.Machine("web", brewSettings)
	e.MustUp("web", "")
	r := e.Check("web")
	r.Has(t, "ok    Homebrew: taps, trust entries and formulae present")
	// a formula and a trust entry vanish
	f := strings.ReplaceAll(e.Srv.File("brew-formulae"), "ripgrep\n", "")
	e.Srv.Write("brew-formulae", f)
	tr := strings.ReplaceAll(e.Srv.File("brew-trust"), "formula acme/tools/widget\n", "")
	e.Srv.Write("brew-trust", tr)
	r = e.Check("web")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "FAIL  Homebrew: missing ripgrep trust:acme/tools/widget")
}

func TestFailingBrewStepsOnlyWarn(t *testing.T) {
	e := New(t, "")
	e.Machine("web", brewSettings)
	e.Srv.Env["MOCK_BREW_FAIL_TAP"] = "acme/tools"
	e.Srv.Env["MOCK_BREW_FAIL_PKG"] = "terraform"
	r := e.Up("web", "")
	if r.Code != 0 {
		t.Fatalf("%d\n%s", r.Code, r.Out)
	}
	r.Has(t, "brew: tap acme/tools FAILED\n", "brew: install ripgrep hashicorp/tap/terraform FAILED", "WARNING: some Homebrew taps, trust entries or formulae failed", "Ready: ssh hetzner-claude-web")
}

func TestBrewListsMaySpanSeveralLines(t *testing.T) {
	e := New(t, "")
	e.Machine("web", "VIPER_BREW_PACKAGES=\"ripgrep\n  fd\n  jq\"\n")
	e.MustUp("web", "").Has(t, "brew: installed ripgrep fd jq\n")
}

func TestCheckFlagsATrustedTapThatIsNotTapped(t *testing.T) {
	e := New(t, "")
	e.Machine("web", "VIPER_BREW_TRUST=\"acme/tpyo\"\n")
	e.MustUp("web", "")
	r := e.Check("web")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "FAIL  Homebrew: missing untapped:acme/tpyo")
}

func TestCredentialsInTapURLsAreRefusedAndNeverShownOrLogged(t *testing.T) {
	e := New(t, "")
	e.Machine("web", "VIPER_BREW_TAPS=\"acme/tools=https://oauth2:tap_secret_7@git.example.invalid/tools.git\"\n")
	r := e.Preflight("web", "")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "credentials in the tap URL (acme/tools=https://***@git.example.invalid/tools.git)")
	r.HasNot(t, "tap_secret_7")
	for _, f := range []string{"events.log", "logs/claude-web.log"} {
		b, _ := os.ReadFile(filepath.Join(e.Home, ".local", "state", "hetzner-claude", f))
		if strings.Contains(string(b), "tap_secret_7") {
			t.Errorf("the secret is in %s", f)
		}
	}
}

func TestPreflightValidatesBrewSettings(t *testing.T) {
	e := New(t, "")
	e.Machine("web", "VIPER_BREW_TAPS=justone\nVIPER_BREW_TRUST=weird:x/y\nVIPER_BREW_PACKAGES=-oops\n")
	r := e.Preflight("web", "")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "not a tap (owner/repo or owner/repo=URL): justone", "VIPER_BREW_TRUST: not a tap", ": weird:x/y", "not a formula", ": -oops")
	e.Machine("web", "VIPER_HOMEBREW=0\nVIPER_BREW_PACKAGES=jq\n")
	e.Preflight("web", "").Has(t, "need VIPER_HOMEBREW=1")
}
