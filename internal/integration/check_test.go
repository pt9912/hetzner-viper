package integration_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// upWeb is a machine with a repository and tokens, like the Bash tests had.
func upWeb(t *testing.T) *Env {
	t.Helper()
	e := New(t, "")
	origin := filepath.Join(t.TempDir(), "origin.git")
	e.gitIn("", "init", "-q", "--bare", origin)
	e.Machine("web", "REPO_URL="+origin+"\nVIPER_CLAUDE_TOKEN=sk-ant-oat01-x\nVIPER_GH_TOKEN=ghp_x\n")
	e.MustUp("web", "")
	return e
}

func (r Result) Match(t *testing.T, re string) {
	t.Helper()
	if !regexp.MustCompile(re).MatchString(r.Out) {
		t.Errorf("no match for %q in\n%s", re, r.Out)
	}
}

func TestCheckOkOnAHealthyMachine(t *testing.T) {
	e := upWeb(t)
	n := len(e.Cloud.Changes())
	r := e.Check("web")
	if r.Code != 0 {
		t.Fatalf("code %d\n%s", r.Code, r.Out)
	}
	r.Has(t, "\nCheck OK.", "ok    Claude Code 2.1.306 (Claude Code): logged in via VIPER_CLAUDE_TOKEN and answers", "ok    gh version 2.46.0", ": logged in", "ok    sshd: public key only")
	r.Match(t, `ok    Cloudflare 1\.1\.1\.1 resolves to 203\.0\.113\.`)
	r.Has(t, "ok    Google 8.8.8.8 resolves to")
	r.Has(t, "ok    connection to claude-web.example.com verified by the host CA")
	r.Match(t, `info  host certificate from .* to `)
	r.Match(t, `ok    repository .*/origin at `)
	if len(e.Cloud.Changes()) != n {
		t.Errorf("check changed something: %v", e.Cloud.Changes()[n:])
	}
}

func TestCheckUsesTheHostCAAndTheDnsNameLikeAnotherDevice(t *testing.T) {
	e := upWeb(t)
	e.Check("web")
	ca := filepath.Join(e.Home, ".ssh", "known_hosts_hetzner_claude_ca")
	found := false
	for _, c := range e.Srv.Calls() {
		if strings.HasPrefix(c, "ssh -n -F /dev/null ") && strings.Contains(c, "UserKnownHostsFile="+ca+" ") && strings.HasSuffix(c, "builder@claude-web.example.com true") {
			found = true
		}
	}
	if !found {
		t.Errorf("by name, CA only, no pinned key:\n%s", strings.Join(e.Srv.Calls(), "\n"))
	}
}

func TestCheckReportsDnsNotResolving(t *testing.T) {
	e := upWeb(t)
	e.DNSStale = true
	r := e.Check("web")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Match(t, `FAIL  Cloudflare 1\.1\.1\.1 resolves to 'nothing', expected 203`)
}

func TestCheckReportsCARejection(t *testing.T) {
	e := upWeb(t)
	e.Srv.Env["MOCK_CA_FAIL"] = "1"
	r := e.Check("web")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "FAIL  connection to claude-web.example.com with the host CA failed: Host key verification failed")
}

func TestCheckReportsAMissingClaudeTokenOnTheServer(t *testing.T) {
	e := upWeb(t)
	_ = os.Remove(filepath.Join(e.Srv.Home, ".config/viper/claude.env")) // may not exist
	r := e.Check("web")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Match(t, `FAIL  Claude Code .*: token not loaded in shells`)
}

func TestCheckReportsWeakSshdAndCloudInitErrors(t *testing.T) {
	e := upWeb(t)
	e.Srv.Env["MOCK_SSHD_WEAK"] = "1"
	e.Srv.Env["MOCK_CLOUDINIT_RC"] = "1"
	r := e.Check("web")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "FAIL  sshd hardening not in effect", "WARN  cloud-init: error")
}

func TestCheckWithoutTokensOnlyInforms(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	r := e.Check("web")
	r.Match(t, `info  Claude Code .*: no VIPER_CLAUDE_TOKEN`)
	r.Match(t, `info  gh version .*: not logged in \(no VIPER_GH_TOKEN\)`)
	r.Has(t, "\nCheck OK.")
}

func TestCheckOfAnAbsentMachine(t *testing.T) {
	e := New(t, "")
	r := e.Check("ghost")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "Server claude-ghost does not exist")
}

func TestCheckExplainsAStaleNegativeAnswerOfTheLocalResolver(t *testing.T) {
	e := upWeb(t)
	e.LocalIP = "none"
	r := e.Check("web")
	r.Match(t, `info  (this computer's|the container's) resolver .*resolves it to 'nothing'`)
	r.Match(t, `remembers 'no such name' .* for up to 1800 s`)
	r.Has(t, "\nCheck OK.") // the public resolvers decide
}

func TestCheckAcceptsCloudInitDisabledAfterTheFirstBoot(t *testing.T) {
	e := upWeb(t)
	e.Srv.Env["MOCK_CLOUDINIT_DISABLED"] = "1"
	e.Check("web").Has(t, "ok    cloud-init: done (disabled after the first boot)")
}

func TestCheckDoesNotAcceptAnErrorThatMentionsOk(t *testing.T) {
	e := upWeb(t)
	e.Srv.Env["MOCK_CLAUDE_ANSWER"] = "API Error: TOKEN expired"
	r := e.Check("web")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Match(t, `FAIL  Claude Code .*: no answer with the token`)
}

func TestCheckReportsFirstBootErrorsAlthoughCloudInitIsDisabled(t *testing.T) {
	e := upWeb(t)
	e.Srv.Env["MOCK_CLOUDINIT_DISABLED"] = "1"
	e.Srv.Write("cloud-result.json", `{"v1": {"errors": ["No valid DHCP lease configuration found"]}}`)
	e.Check("web").Has(t, "WARN  cloud-init reported 1 error(s) at the first boot, e.g.: No valid DHCP lease")
}

func TestCheckReportsHomebrew(t *testing.T) {
	e := upWeb(t)
	e.Check("web").Has(t, "ok    Homebrew 4.9.0")
	_ = os.Remove(e.Srv.BrewPath) // may not exist
	r := e.Check("web")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "FAIL  Homebrew not installed")
}

func TestUpAndDownAreLogged(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	ev := e.Events()
	for _, re := range []string{
		` claude-web +up +start \(cpx32, fsn1, ubuntu-26\.04\)\n`, ` claude-web +up +server created id=[0-9]+\n`,
		` claude-web +up +ip=203\.0\.113\.[0-9]+ dns=claude-web\.example\.com\n`, ` claude-web +up +ready \(work dir `,
	} {
		if !regexp.MustCompile(re).MatchString(ev) {
			t.Errorf("events lack %q:\n%s", re, ev)
		}
	}
	e.Down("web", false, "DELETE")
	ev = e.Events()
	if !regexp.MustCompile(` claude-web +down +server deleted id=[0-9]+ ip=`).MatchString(ev) || !regexp.MustCompile(` claude-web +down +deleted\n`).MatchString(ev) {
		t.Errorf("events:\n%s", ev)
	}
	tr := e.Log("claude-web")
	if n := len(regexp.MustCompile(`(?m)^===== .* up claude-web$`).FindAllString(tr, -1)); n != 1 || !regexp.MustCompile(`(?m)^===== .* down claude-web$`).MatchString(tr) {
		t.Errorf("transcript has both runs: %d up headers\n%s", n, tr)
	}
	if !strings.Contains(tr, "Ready: ssh hetzner-claude-web") {
		t.Error("output not in the transcript")
	}
	if n := strings.Count(tr, "\nPreflight OK.\n"); n != 1 {
		t.Errorf("nested preflight logged %d times", n)
	}
	for _, f := range []string{"events.log", "logs/claude-web.log"} {
		if m := mode(t, filepath.Join(e.Home, ".local", "state", "hetzner-claude", f)); m != 0o600 {
			t.Errorf("%s mode %v", f, m)
		}
	}
}

func TestFailuresAndCancellationsAreLogged(t *testing.T) {
	e := New(t, "")
	e.Srv.Env["MOCK_SSH_DOWN"] = "1"
	e.Up("web", "")
	if !regexp.MustCompile(` claude-web +up +FAILED \(exit 1; see logs/claude-web\.log\)\n`).MatchString(e.Events()) {
		t.Errorf("events:\n%s", e.Events())
	}
	if !strings.Contains(e.Log("claude-web"), "SSH not reachable; server retained") {
		t.Error("the error is not in the transcript")
	}
	delete(e.Srv.Env, "MOCK_SSH_DOWN")
	e.MustUp("web", "")
	e.Down("web", false, "no")
	if !regexp.MustCompile(` claude-web +down +cancelled \(no DELETE\)\n`).MatchString(e.Events()) {
		t.Errorf("events:\n%s", e.Events())
	}
}

func TestCheckAndPreflightAreLogged(t *testing.T) {
	e := New(t, "")
	e.Preflight("web", "")
	e.MustUp("web", "")
	e.Check("web")
	ev := e.Events()
	if !regexp.MustCompile(` claude-web +preflight +preflight OK\n`).MatchString(ev) || !regexp.MustCompile(` claude-web +check +check OK\n`).MatchString(ev) {
		t.Errorf("events:\n%s", ev)
	}
	if n := strings.Count(ev, "preflight OK\n"); n != 1 {
		t.Errorf("preflight logged %d times (only the standalone run)", n)
	}
}

func TestLogsContainNoTokens(t *testing.T) {
	e := New(t, "")
	e.Tokens["HCLOUD_TOKEN"] = "hc_secret_1"
	e.Tokens["CLOUDFLARE_API_TOKEN"] = "cf_secret_2"
	e.Tokens["VIPER_GH_TOKEN"] = "gh_secret_3"
	e.Tokens["VIPER_CLAUDE_TOKEN"] = "sk-ant-oat01-secret_4"
	e.MustUp("web", "")
	e.Check("web")
	e.Down("web", false, "DELETE")
	err := filepath.Walk(filepath.Join(e.Home, ".local", "state", "hetzner-claude"), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, _ := os.ReadFile(p)
		if regexp.MustCompile(`secret_[0-9]`).Match(b) {
			t.Errorf("a token is in %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
