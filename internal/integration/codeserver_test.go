package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func csSettings(password string) string {
	if password == "" {
		password = "pw-with-12-chars"
	}
	// the file is read like bash reads it: quote the value
	return "VIPER_CODE_SERVER=1\nVIPER_CODE_SERVER_PASSWORD='" + strings.ReplaceAll(password, "'", `'\''`) + "'\n"
}

func TestCodeServerIsOffByDefault(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	for _, c := range e.Srv.Calls() {
		if strings.Contains(c, "code-server") {
			t.Errorf("code-server was touched: %s", c)
		}
	}
	if e.Srv.File("systemctl.log") != "" {
		t.Error("a unit was touched")
	}
}

func TestCodeServerIsInstalledConfiguredAndStarted(t *testing.T) {
	e := New(t, "")
	pw := `p@ss"w0rd"\#1:2-ok`
	e.Machine("web", csSettings(pw))
	r := e.MustUp("web", "")
	cfg := e.ServerFile(".config/code-server/config.yaml")
	for _, want := range []string{"bind-addr: 127.0.0.1:8080\n", "auth: password\n", "cert: false\n"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config lacks %q:\n%s", want, cfg)
		}
	}
	// the file is YAML: any character of the password must survive (JSON strings are YAML strings)
	var line string
	for _, l := range strings.Split(cfg, "\n") {
		if strings.HasPrefix(l, "password: ") {
			line = strings.TrimPrefix(l, "password: ")
		}
	}
	if line != `"p@ss\"w0rd\"\\#1:2-ok"` {
		t.Errorf("password line %s", line)
	}
	if m := mode(t, filepath.Join(e.Srv.Home, ".config/code-server/config.yaml")); m != 0o600 {
		t.Errorf("mode %v", m)
	}
	sc := e.Srv.File("systemctl.log")
	if !strings.Contains(sc, "systemctl enable code-server@builder") || !strings.Contains(sc, "systemctl restart code-server@builder") {
		t.Errorf("systemctl:\n%s", sc)
	}
	r.Has(t, "code-server: running on the server at 127.0.0.1:8080; tunnel: ssh -L 8080:127.0.0.1:8080 hetzner-claude-web", "code-server: 4.96.4")
	found := false
	for _, c := range e.Srv.Calls() {
		if strings.Contains(c, "code-server.dev/install.sh") {
			found = true
		}
	}
	if !found {
		t.Error("the official installer was not used")
	}
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

func TestCodeServerPasswordNeverInArgvLogsOrUserData(t *testing.T) {
	e := New(t, "")
	e.Machine("web", csSettings("secret-pw-0042"))
	e.MustUp("web", "")
	for _, c := range e.Srv.Calls() {
		if strings.Contains(c, "secret-pw-0042") {
			t.Errorf("in a call: %s", c)
		}
	}
	for _, f := range []string{"events.log", "logs/claude-web.log"} {
		b, _ := os.ReadFile(filepath.Join(e.Home, ".local", "state", "hetzner-claude", f))
		if strings.Contains(string(b), "secret-pw-0042") {
			t.Errorf("in %s", f)
		}
	}
	if strings.Contains(e.Cloud.UserData["claude-web"], "secret-pw-0042") {
		t.Error("in the user-data")
	}
}

func TestCodeServerNeedsAGoodPassword(t *testing.T) {
	e := New(t, "")
	for settings, want := range map[string]string{
		"VIPER_CODE_SERVER=1\n":                         "VIPER_CODE_SERVER=1 needs VIPER_CODE_SERVER_PASSWORD",
		csSettings("short"):                             "at least 12 characters",
		csSettings("has a space-1234"):                  "no spaces or line breaks",
		"VIPER_CODE_SERVER=yes\n":                       "VIPER_CODE_SERVER must be 0 or 1: yes",
		"VIPER_CODE_SERVER_PASSWORD=pw-with-12-chars\n": "info  VIPER_CODE_SERVER_PASSWORD is set but VIPER_CODE_SERVER is not 1",
		csSettings(""):                                  "ok    code-server will be installed (VS Code in the browser, reachable only through an SSH tunnel to 127.0.0.1:8080)",
	} {
		e.Machine("web", settings)
		e.Preflight("web", "").Has(t, want)
	}
}

func TestCodeServerFailuresAreNotFatal(t *testing.T) {
	e := New(t, "")
	e.Machine("web", csSettings(""))
	e.Srv.Env["MOCK_CODE_SERVER_FAIL"] = "1"
	r := e.Up("web", "")
	if r.Code != 0 {
		t.Fatalf("%d\n%s", r.Code, r.Out)
	}
	r.Has(t, "WARNING: code-server installation failed", "Ready: ssh hetzner-claude-web")
	if e.ServerFile(".config/code-server/config.yaml") != "" {
		t.Error("config without the program")
	}
	delete(e.Srv.Env, "MOCK_CODE_SERVER_FAIL")
	os.Remove(filepath.Join(e.Srv.State, "code-server-installed"))
	e.Srv.Env["MOCK_SYSTEMCTL_FAIL"] = "1"
	r = e.Up("web", "")
	r.Has(t, "WARNING: code-server could not be configured or started", "Ready: ssh hetzner-claude-web")
}

func TestCheckReportsCodeServer(t *testing.T) {
	e := New(t, "")
	e.Machine("web", csSettings(""))
	e.MustUp("web", "")
	e.Check("web").Has(t, "ok    code-server 4.96.4 abcdef123 with Code 1.96.4 on 127.0.0.1:8080 (tunnel: ssh -L 8080:127.0.0.1:8080 hetzner-claude-web)")
	e.Srv.Env["MOCK_CODE_SERVER_DOWN"] = "1"
	r := e.Check("web")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "FAIL  code-server 4.96.4 abcdef123 with Code 1.96.4 is not running")
	delete(e.Srv.Env, "MOCK_CODE_SERVER_DOWN")
	os.Remove(filepath.Join(e.Srv.State, "code-server-installed"))
	e.Check("web").Has(t, "FAIL  code-server not installed")
	e.Machine("web", "VIPER_CODE_SERVER=0\n")
	if r := e.Check("web"); strings.Contains(r.Out, "code-server") {
		t.Errorf("nothing checked when off:\n%s", r.Out)
	}
}
