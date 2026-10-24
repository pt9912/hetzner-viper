package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func gatewaySettings(model string) string {
	if model == "" {
		model = "minimax-m3.1-flash"
	}
	return "VIPER_ANTHROPIC_BASE_URL=https://llm.test.invalid\nVIPER_ANTHROPIC_AUTH_TOKEN=olk_secret_9\nVIPER_CLAUDE_MODEL=" + model + "\n"
}

func TestGatewaySettingsReachTheShellsOnTheServer(t *testing.T) {
	e := New(t, "")
	e.Machine("web", gatewaySettings("")+"VIPER_CLAUDE_MAX_CONTEXT_TOKENS=1048576\n")
	r := e.MustUp("web", "")
	r.Has(t, "claude: using model minimax-m3.1-flash at https://llm.test.invalid")
	for name, want := range map[string]string{
		"ANTHROPIC_BASE_URL": "https://llm.test.invalid", "ANTHROPIC_AUTH_TOKEN": "olk_secret_9", "ANTHROPIC_API_KEY": "",
		"ANTHROPIC_MODEL": "minimax-m3.1-flash", "ANTHROPIC_DEFAULT_OPUS_MODEL": "minimax-m3.1-flash", "ANTHROPIC_DEFAULT_SONNET_MODEL": "minimax-m3.1-flash",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL": "minimax-m3.1-flash", "CLAUDE_CODE_SUBAGENT_MODEL": "minimax-m3.1-flash", "CLAUDE_CODE_MAX_CONTEXT_TOKENS": "1048576",
	} {
		if got := e.RemoteEnv(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	for _, c := range e.Srv.Calls() {
		if strings.Contains(c, "olk_secret_9") {
			t.Errorf("token on a command line: %s", c)
		}
	}
}

func TestSwitchingFromSubscriptionToGatewayDropsTheOauthToken(t *testing.T) {
	e := New(t, "")
	e.Tokens["VIPER_CLAUDE_TOKEN"] = "sk-ant-oat01-x"
	e.MustUp("web", "")
	if got := e.RemoteEnv("CLAUDE_CODE_OAUTH_TOKEN"); got != "sk-ant-oat01-x" {
		t.Errorf("first: subscription: %q", got)
	}
	delete(e.Tokens, "VIPER_CLAUDE_TOKEN")
	e.Machine("web", gatewaySettings(""))
	e.Gateway("minimax-m3.1-flash")
	e.MustUp("web", "")
	if got := e.RemoteEnv("CLAUDE_CODE_OAUTH_TOKEN"); got != "<unset>" {
		t.Errorf("then: no oauth token: %q", got)
	}
}

func TestPreflightChecksTheGatewayWithARealRequest(t *testing.T) {
	e := New(t, "")
	e.Machine("web", gatewaySettings("glm-5.3-flash[1m]"))
	e.Gateway("glm-5.3-flash")
	e.Preflight("web", "").Has(t, "ok    https://llm.test.invalid answers with model glm-5.3-flash ")
	e.Machine("web", strings.Replace(gatewaySettings("glm-5.3-flash"), "olk_secret_9", "wrong", 1))
	r := e.Preflight("web", "")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "rejects VIPER_ANTHROPIC_AUTH_TOKEN (HTTP 401: invalid api key)")
	e.Machine("web", gatewaySettings("glm-5.3-flash"))
	e.Gateway("other-model")
	e.Preflight("web", "").Has(t, "model glm-5.3-flash or the Messages API not found (HTTP 404: model not found)")
}

func TestGatewayAndSubscriptionTokenTogetherFailBeforeAnything(t *testing.T) {
	e := New(t, "")
	e.Machine("web", gatewaySettings("")+"VIPER_CLAUDE_TOKEN=sk-ant-oat01-x\n")
	e.Gateway("minimax-m3.1-flash")
	r := e.Up("web", "")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "set either VIPER_CLAUDE_TOKEN or VIPER_ANTHROPIC_BASE_URL, not both")
	if len(e.Cloud.Servers) != 0 || len(e.Cloud.Changes()) != 0 {
		t.Errorf("something was created: %v", e.Cloud.Changes())
	}
}

func TestGatewayNeedsTokenAndModelAndAUrl(t *testing.T) {
	e := New(t, "")
	e.Machine("web", "VIPER_ANTHROPIC_BASE_URL=llm.test.invalid\n")
	e.Preflight("web", "").Has(t, "VIPER_ANTHROPIC_AUTH_TOKEN is required", "VIPER_CLAUDE_MODEL is required", "must start with https://")
}

func TestPlainHttpGatewayIsWarnedAbout(t *testing.T) {
	e := New(t, "")
	e.Machine("web", strings.Replace(gatewaySettings(""), "https://llm", "http://llm", 1))
	e.Preflight("web", "").Has(t, "WARN  VIPER_ANTHROPIC_BASE_URL is not https")
}

func TestCheckReportsTheGatewayModel(t *testing.T) {
	e := New(t, "")
	e.Machine("web", gatewaySettings(""))
	e.Gateway("minimax-m3.1-flash")
	e.MustUp("web", "")
	r := e.Check("web")
	r.Has(t, "model minimax-m3.1-flash at https://llm.test.invalid answers", "\nCheck OK.")
}

func TestVSCodeDoesNotGetTheGatewayToken(t *testing.T) {
	e := New(t, "")
	e.Machine("web", gatewaySettings(""))
	e.Gateway("minimax-m3.1-flash")
	e.MustUp("web", "")
	if len(e.VSCode) != 1 {
		t.Errorf("vscode %v", e.VSCode)
	}
}

func TestGatewayUrlWithCredentialsIsNotShown(t *testing.T) {
	e := New(t, "")
	e.Machine("web", "VIPER_ANTHROPIC_BASE_URL=https://user:gw_secret_5@llm.test.invalid\nVIPER_ANTHROPIC_AUTH_TOKEN=k\nVIPER_CLAUDE_MODEL=m\n")
	up := e.Up("web", "")
	chk := e.Check("web")
	all := up.Out + chk.Out
	if !strings.Contains(all, "https://***@llm.test.invalid") {
		t.Errorf("not redacted:\n%s", all)
	}
	if strings.Contains(all, "gw_secret_5") {
		t.Error("the credential is shown")
	}
	for _, f := range []string{"events.log", "logs/claude-web.log"} {
		b, _ := os.ReadFile(filepath.Join(e.Home, ".local", "state", "hetzner-claude", f))
		if strings.Contains(string(b), "gw_secret_5") {
			t.Errorf("in %s", f)
		}
	}
}
