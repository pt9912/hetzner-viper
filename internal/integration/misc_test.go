package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCredentialsInTheRepoUrlAreRefusedAndNeverLogged(t *testing.T) {
	e := New(t, "")
	r := e.Up("web", "https://oauth2:tok_secret_9@git.example.invalid/o/r.git")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "REPO_URL contains credentials (https://***@git.example.invalid/o/r.git)")
	for _, f := range []string{"events.log", "logs/claude-web.log"} {
		if strings.Contains(e.state(f), "tok_secret_9") {
			t.Errorf("the credential is in %s", f)
		}
	}
	if len(e.Cloud.Servers) != 0 {
		t.Error("a server was created")
	}
}

func TestInventoryOfTheServerIsLogged(t *testing.T) {
	e := New(t, "")
	e.Machine("web", "VIPER_BREW_TAPS=\"acme/tools\"\nVIPER_BREW_TRUST=\"acme/tools\"\nVIPER_BREW_PACKAGES=\"ripgrep acme/tools/widget\"\n")
	e.MustUp("web", "")
	tr := e.Log("claude-web")
	for _, re := range []string{
		`(?m)^== installed on the server ==$`, `(?m)^os: .*, kernel `, `(?m)^homebrew: Homebrew `, `(?m)^brew taps: .*acme/tools`,
		`(?m)^brew trusted: \{"taps":\["acme/tools"\]\}`, `(?m)^brew formulae \(installed on request\): ripgrep 1\.0, widget 1\.0$`,
	} {
		if !regexp.MustCompile(re).MatchString(tr) {
			t.Errorf("transcript lacks %q:\n%s", re, tr)
		}
	}
	found := false
	for _, c := range e.Srv.Calls() {
		if regexp.MustCompile(`bash -s -- .* 'git' 'gh' 'make' 'build-essential'`).MatchString(c) {
			found = true
		}
	}
	if !found {
		t.Error("the apt list of cloud-init.yaml was not asked for")
	}
}

func TestPreflightIsOkAndReadOnly(t *testing.T) {
	e := New(t, "")
	r := e.Preflight("web", "")
	if r.Code != 0 {
		t.Fatalf("%d\n%s", r.Code, r.Out)
	}
	r.Has(t, "\nPreflight OK.", "cpx32 can currently be ordered in fsn1", "server type cpx32 exists (4 vCPU, 8 GB RAM, 160 GB disk), architecture x86")
	if len(e.Cloud.Changes()) != 0 {
		t.Errorf("preflight changed something: %v", e.Cloud.Changes())
	}
}

func TestAnUnreachableRepositoryFailsBeforeAnythingIsCreated(t *testing.T) {
	e := New(t, "")
	r := e.Up("web", filepath.Join(t.TempDir(), "does-not-exist.git"))
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "not reachable without credentials")
	if len(e.Cloud.Changes()) != 0 {
		t.Errorf("created %v", e.Cloud.Changes())
	}
}

func TestADnsDomainOutsideTheZoneFails(t *testing.T) {
	e := New(t, "")
	e.Cloud.DNSZone = "other.invalid"
	r := e.Preflight("web", "")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "not in Cloudflare zone other.invalid")
}

func TestMissingSettingsAreReportedTogetherBeforeAnything(t *testing.T) {
	e := New(t, "")
	delete(e.Tokens, "CLOUDFLARE_API_TOKEN")
	delete(e.Tokens, "CLOUDFLARE_ZONE_ID")
	// the helpers load the configuration with the tokens required: this is what the command does
	_, err := configLoad(e, "web")
	if err == nil || !strings.Contains(err.Error(), "Missing settings: CLOUDFLARE_API_TOKEN CLOUDFLARE_ZONE_ID") {
		t.Errorf("got %v", err)
	}
	if len(e.Cloud.Changes()) != 0 {
		t.Error("something was created")
	}
}

func TestStopWithoutAZoneIDFailsBeforeDeleting(t *testing.T) {
	e := boot(t)
	delete(e.Tokens, "CLOUDFLARE_ZONE_ID")
	if _, err := configLoad(e, "web"); err == nil || !strings.Contains(err.Error(), "Missing settings: CLOUDFLARE_ZONE_ID") {
		t.Errorf("got %v", err)
	}
	if !e.kept() {
		t.Error("the server is gone")
	}
}

func TestLogTimesAreLocalWithAnOffset(t *testing.T) {
	loc := berlin(t)
	old := timeLocal()
	setTimeLocal(loc)
	defer setTimeLocal(old)
	e := New(t, "")
	e.Preflight("web", "")
	if !regexp.MustCompile(`(?m)^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\+0[12]:00 claude-web `).MatchString(e.Events()) {
		t.Errorf("events:\n%s", e.Events())
	}
	if !regexp.MustCompile(`(?m)^===== [0-9-]{10}T[0-9:]{8}\+0[12]:00 preflight claude-web$`).MatchString(e.Log("claude-web")) {
		t.Errorf("transcript header:\n%s", e.Log("claude-web"))
	}
}

func TestCheckOfAMachineWithSwapCanBeTurnedOff(t *testing.T) {
	// VIPER_SWAP=0 is the default: nothing about swap is checked
	e := New(t, "")
	e.MustUp("web", "")
	if r := e.Check("web"); strings.Contains(r.Out, "swap") {
		t.Errorf("swap checked:\n%s", r.Out)
	}
	_ = os.Stdout
}
