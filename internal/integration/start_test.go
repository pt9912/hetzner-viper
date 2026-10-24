package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// userData is the user-data of the server as YAML.
func (e *Env) userData(server string) map[string]any {
	e.t.Helper()
	var d map[string]any
	if err := yaml.Unmarshal([]byte(e.Cloud.UserData[server]), &d); err != nil {
		e.t.Fatalf("the user-data is not YAML: %v\n%s", err, e.Cloud.UserData[server])
	}
	return d
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func (e *Env) changes(prefix string) int {
	n := 0
	for _, c := range e.Cloud.Changes() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (e *Env) sshConfigFile(machine string) string {
	b, _ := os.ReadFile(filepath.Join(e.Home, ".ssh", "config_hetzner_claude-"+machine))
	return string(b)
}

func TestStartCreatesAllResourcesForAMachine(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	if len(e.Cloud.Created) != 1 || e.Cloud.Created[0].Name != "claude-web" {
		t.Fatalf("created %v", e.Cloud.Created)
	}
	if e.changes("firewall create claude-ssh") != 1 {
		t.Errorf("changes %v", e.Cloud.Changes())
	}
	if rec, ok := e.Cloud.DNSRecords["claude-web.example.com"]; !ok || !rec.Ours || !strings.HasPrefix(rec.IP, "203.0.113.") {
		t.Errorf("dns %+v", e.Cloud.DNSRecords)
	}
	cfg := e.sshConfigFile("web")
	if !regexp.MustCompile(`HostName 203\.0\.113\.`).MatchString(cfg) || !strings.Contains(cfg, "HostKeyAlias claude-web.example.com") {
		t.Errorf("ssh config:\n%s", cfg)
	}
	inc, _ := os.ReadFile(filepath.Join(e.Home, ".ssh", "config"))
	if !strings.Contains(string(inc), "Include "+filepath.Join(e.Home, ".ssh", "config_hetzner_claude-web")) {
		t.Errorf("~/.ssh/config:\n%s", inc)
	}
	ws := e.Config("web").WorkspaceDir
	found := false
	for _, c := range e.Srv.Calls() {
		if strings.Contains(c, "install -d -o builder -g builder '"+ws+"'") {
			found = true
		}
	}
	if !found {
		t.Error("the work directory was not created for builder")
	}
	if !strings.Contains(strings.Join(e.Cloud.Changes(), "\n"), "server create claude-web --type cpx32") {
		t.Errorf("changes %v", e.Cloud.Changes())
	}
}

func TestUserDataCarriesTheSignedHostKeyViaSshKeys(t *testing.T) {
	tool(t, "ssh-keygen")
	e := New(t, "")
	e.MustUp("web", "")
	d := e.userData("claude-web")
	keys, _ := d["ssh_keys"].(map[string]any)
	if len(keys) != 3 || keys["ed25519_private"] == nil || keys["ed25519_public"] == nil || keys["ed25519_certificate"] == nil {
		t.Fatalf("ssh_keys %v", keys)
	}
	users := list(d["users"])
	if len(users) != 1 {
		t.Fatalf("users %v", users)
	}
	u := users[0].(map[string]any)
	auth := list(u["ssh_authorized_keys"])
	if u["name"] != "builder" || len(auth) != 1 || !strings.HasPrefix(auth[0].(string), "ssh-ed25519 ") {
		t.Errorf("user %v", u)
	}
	for _, w := range list(d["write_files"]) {
		if strings.HasPrefix(w.(map[string]any)["path"].(string), "/etc/ssh/ssh_host_") {
			t.Errorf("the host key must not come via write_files: %v", w)
		}
	}
	certFile := filepath.Join(t.TempDir(), "cert.pub")
	if err := os.WriteFile(certFile, []byte(keys["ed25519_certificate"].(string)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ssh-keygen", "-L", "-f", certFile).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(string(out), "host certificate") || !strings.Contains(string(out), "claude-web.example.com") {
		t.Errorf("certificate:\n%s", out)
	}
	fp, _ := exec.Command("ssh-keygen", "-lf", filepath.Join(e.Home, ".ssh", "hetzner_claude_host_ca.pub")).Output()
	if !strings.Contains(string(out), strings.Fields(string(fp))[1]) {
		t.Errorf("not signed by our CA:\n%s", out)
	}
	// the host key comment does not leak the local user
	if strings.Contains(keys["ed25519_public"].(string), "@") {
		t.Errorf("host key comment: %v", keys["ed25519_public"])
	}
}

func TestSecondMachineSharesKeyAndFirewall(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	e.MustUp("api", "")
	if e.changes("firewall create") != 1 || e.changes("ssh-key create") != 1 || e.changes("server create") != 2 {
		t.Errorf("changes %v", e.Cloud.Changes())
	}
	for _, h := range []string{"claude-api.example.com", "claude-web.example.com"} {
		if _, ok := e.Cloud.DNSRecords[h]; !ok {
			t.Errorf("no record for %s", h)
		}
	}
	if _, err := os.Stat(filepath.Join(e.Home, ".ssh", "config_hetzner_claude-api")); err != nil {
		t.Error("own ssh config")
	}
}

func TestRestartOfARunningMachineIsIdempotent(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	r := e.MustUp("web", "")
	r.Has(t, "Existing server: reusing it.")
	if e.changes("server create") != 1 || len(e.Cloud.DNSRecords) != 1 {
		t.Errorf("changes %v, dns %v", e.Cloud.Changes(), e.Cloud.DNSRecords)
	}
}

func originRepo(t *testing.T) string {
	t.Helper()
	origin := filepath.Join(t.TempDir(), "origin.git")
	seed := filepath.Join(t.TempDir(), "seed")
	for _, a := range [][]string{{"init", "-q", "--bare", origin}, {"clone", "-q", origin, seed}} {
		if out, err := gitCmd("", a...); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	for _, a := range [][]string{{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"}, {"push", "-q", "origin", "HEAD:main"}} {
		if out, err := gitCmd(seed, a...); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	return origin
}

func TestStartClonesTheRepositoryOnce(t *testing.T) {
	e := New(t, "")
	origin := originRepo(t)
	e.MustUp("web", origin)
	ws := e.Config("web").WorkspaceDir
	if _, err := os.Stat(filepath.Join(ws, "origin", ".git")); err != nil {
		t.Fatalf("not cloned into the work directory: %v", err)
	}
	if len(e.VSCode) != 1 || e.VSCode[0] != "hetzner-claude-web "+filepath.Join(ws, "origin") {
		t.Errorf("vscode %v", e.VSCode)
	}
	e.MustUp("web", origin).Has(t, "Repository already present: "+filepath.Join(ws, "origin"))
}

func TestRepositoryFromTheMachineFileWithADeployKey(t *testing.T) {
	e := New(t, "")
	origin := originRepo(t)
	deploy := filepath.Join(e.Home, ".ssh", "deploy")
	if err := os.WriteFile(deploy, []byte("key-material\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.Machine("web", "REPO_URL="+origin+"\nGIT_DEPLOY_KEY_FILE="+deploy+"\n")
	e.MustUp("web", "")
	if got := e.Srv.File("scp/deploy_key"); got != "key-material\n" {
		t.Errorf("key copied: %q", got)
	}
	ws := e.Config("web").WorkspaceDir
	cfg, _ := os.ReadFile(filepath.Join(ws, "origin", ".git", "config"))
	if !strings.Contains(string(cfg), "deploy_key") {
		t.Errorf("core.sshCommand not set:\n%s", cfg)
	}
}

func TestMachineFileOverridesTheServerType(t *testing.T) {
	e := New(t, "")
	e.Machine("small", "SERVER_TYPE=cpx02\n")
	e.MustUp("small", "")
	e.MustUp("big", "")
	if e.Cloud.Created[0].Type != "cpx02" || e.Cloud.Created[1].Type != "cpx32" {
		t.Errorf("types %s, %s", e.Cloud.Created[0].Type, e.Cloud.Created[1].Type)
	}
}

func TestStartWithoutSetupFailsCleanly(t *testing.T) {
	e := New(t, "")
	_ = os.Remove(filepath.Join(e.Home, ".ssh", "hetzner_claude_host_ca")) // may not exist
	r := e.Up("web", "")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "Run viper setup first")
	if len(e.Cloud.Changes()) != 0 {
		t.Errorf("created: %v", e.Cloud.Changes())
	}
}

func TestDnsRefusesAForeignRecordBeforeAnythingIsCreated(t *testing.T) {
	e := New(t, "")
	e.Cloud.DNSRecords["claude-web.example.com"] = servertestRecord("198.51.100.1", false)
	r := e.Up("web", "")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "not managed by this setup")
	if len(e.Cloud.Changes()) != 0 || e.Cloud.DNSRecords["claude-web.example.com"].IP != "198.51.100.1" {
		t.Errorf("changes %v, record %+v", e.Cloud.Changes(), e.Cloud.DNSRecords)
	}
}

func TestCloudInitWarningsAndErrors(t *testing.T) {
	e := New(t, "")
	e.Srv.Env["MOCK_CLOUDINIT_RC"] = "2"
	if r := e.Up("web", ""); r.Code != 0 {
		t.Errorf("done with warnings is ready: %d\n%s", r.Code, r.Out)
	}
	e2 := New(t, "")
	e2.Srv.Env["MOCK_CLOUDINIT_RC"] = "1"
	r := e2.Up("web", "")
	if r.Code != 0 {
		t.Fatalf("%d\n%s", r.Code, r.Out)
	}
	r.Has(t, "cloud-init reported errors")
}

func TestDockerNotRunningFails(t *testing.T) {
	e := New(t, "")
	e.Srv.Env["MOCK_DOCKER_FAIL"] = "1"
	r := e.Up("web", "")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "Docker is not running")
}

func TestSshNeverReachableFailsAndKeepsTheServer(t *testing.T) {
	e := New(t, "")
	e.Srv.Env["MOCK_SSH_DOWN"] = "1"
	r := e.Up("web", "")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "SSH not reachable")
	if e.Cloud.Servers["claude-web"] == nil {
		t.Error("the server must stay")
	}
}

func TestMissingClaudeInstallIsRepairedByStart(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	_ = os.Remove(filepath.Join(e.Srv.State, "claude-ready")) // may not exist
	n := len(e.Srv.Calls())
	e.MustUp("web", "")
	installs := 0
	for _, c := range e.Srv.Calls()[n:] {
		if strings.Contains(c, "claude.ai/install.sh") {
			installs++
		}
	}
	if installs != 1 || !e.Srv.Exists("claude-ready") {
		t.Errorf("installer runs %d, marker %v", installs, e.Srv.Exists("claude-ready"))
	}
}

func TestFailedClaudeInstallIsReported(t *testing.T) {
	e := New(t, "")
	e.Srv.Env["MOCK_INSTALL_FAIL"] = "1"
	r := e.Up("web", "")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "installation failed")
}

func TestClaudeInstallIsRetriedWhenRateLimited(t *testing.T) {
	e := New(t, "")
	e.Srv.Env["MOCK_INSTALL_FAIL_TIMES"] = "2"
	r := e.MustUp("web", "")
	r.Has(t, "installation failed (attempt 1 of 5); retrying in 30 s", "attempt 2 of 5); retrying in 60 s", "Ready: ssh hetzner-claude-web")
	if !e.Srv.Exists("claude-ready") {
		t.Error("the third attempt worked")
	}
	e2 := New(t, "")
	e2.Srv.Env["MOCK_INSTALL_FAIL_TIMES"] = "9"
	r = e2.Up("web", "")
	if r.Code != 1 || strings.TrimSpace(e2.Srv.File("install-attempts")) != "5" {
		t.Errorf("code %d, attempts %q", r.Code, e2.Srv.File("install-attempts"))
	}
	r.Has(t, "rate limit, HTTP 429")
}

func TestCloudInitMarksReadyOnlyAfterASuccessfulInstall(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	d := e.userData("claude-web")
	var install []string
	for _, c := range list(d["runcmd"]) {
		if s, ok := c.(string); ok && strings.Contains(s, "install.sh") {
			install = append(install, s)
		}
		if s, ok := c.(string); ok && s == "touch /var/tmp/claude-ready" {
			t.Error("the marker must only be set after a successful install")
		}
	}
	if len(install) != 1 || !strings.Contains(install[0], "pipefail") || !strings.Contains(install[0], "&& touch /var/tmp/claude-ready && break") ||
		!strings.HasPrefix(install[0], "for i in") || !strings.Contains(install[0], "[ $i -lt 5 ] && sleep") {
		t.Errorf("install command %v", install)
	}
	for _, w := range list(d["write_files"]) {
		if strings.Contains(w.(map[string]any)["path"].(string), "60-host-cert") {
			t.Error("host cert via write_files")
		}
	}
	found := false
	for _, c := range list(d["runcmd"]) {
		if c == "systemctl try-reload-or-restart ssh" {
			found = true
		}
	}
	if !found {
		t.Error("sshd is reloaded")
	}
}

func TestFirewallWithoutRulesGetsSshAndIcmpRules(t *testing.T) {
	e := New(t, "")
	e.Cloud.Firewalls["claude-ssh"] = nil
	e.MustUp("web", "")
	if e.changes("firewall add-rule claude-ssh tcp 22") != 1 || e.changes("firewall add-rule claude-ssh icmp") != 1 {
		t.Errorf("changes %v", e.Cloud.Changes())
	}
	e.MustUp("api", "")
	if e.changes("firewall add-rule") != 2 {
		t.Errorf("no further rules: %v", e.Cloud.Changes())
	}
}

func TestExistingFirewallWithOnlySshGetsIcmp(t *testing.T) {
	e := New(t, "SSH_ALLOWED_CIDRS=10.0.0.0/8\n")
	e.Cloud.Firewalls["claude-ssh"] = []firewallRule{{Direction: "in", Protocol: "tcp", Port: "22", SourceIPs: []string{"10.0.0.0/8"}}}
	e.MustUp("web", "")
	if e.changes("firewall add-rule claude-ssh icmp  10.0.0.0/8") != 1 || e.changes("firewall add-rule claude-ssh tcp") != 0 {
		t.Errorf("changes %v", e.Cloud.Changes())
	}
}

func TestMissingDeployKeyFailsBeforeCreatingAnything(t *testing.T) {
	e := New(t, "")
	e.Machine("web", "REPO_URL=git@example.org:o/r.git\nGIT_DEPLOY_KEY_FILE="+filepath.Join(t.TempDir(), "nope")+"\n")
	r := e.Up("web", "")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "File not readable")
	if len(e.Cloud.Servers) != 0 || len(e.Cloud.Changes()) != 0 {
		t.Errorf("created %v", e.Cloud.Changes())
	}
}

func TestScriptsUseTheMachinesOwnSshConfigOnly(t *testing.T) {
	e := New(t, "")
	origin := originRepo(t)
	deploy := filepath.Join(e.Home, ".ssh", "deploy")
	if err := os.WriteFile(deploy, []byte("k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.Machine("web", "REPO_URL="+origin+"\nGIT_DEPLOY_KEY_FILE="+deploy+"\n")
	e.MustUp("web", "")
	e.Down("web", false, "DELETE")
	cfg := filepath.Join(e.Home, ".ssh", "config_hetzner_claude-web")
	n := 0
	for _, c := range e.Srv.Calls() {
		if strings.HasPrefix(c, "ssh ") || strings.HasPrefix(c, "scp ") {
			n++
			if !strings.Contains(c, "-F "+cfg) {
				t.Errorf("a call without the machine's config: %s", c)
			}
		}
	}
	if n == 0 {
		t.Error("no calls")
	}
}

func TestGhIsInstalledByCloudInit(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	found := false
	for _, p := range list(e.userData("claude-web")["packages"]) {
		if p == "gh" {
			found = true
		}
	}
	if !found {
		t.Error("gh is not in the packages")
	}
}

func TestGhTokenIsPassedViaStdinBeforeTheClone(t *testing.T) {
	e := New(t, "")
	origin := originRepo(t)
	e.Tokens["VIPER_GH_TOKEN"] = "ghp_secret123"
	e.MustUp("web", origin)
	if got := e.Srv.File("gh-token"); strings.TrimSpace(got) != "ghp_secret123" {
		t.Errorf("token reached gh: %q", got)
	}
	login, clone := -1, -1
	for i, c := range e.Srv.Calls() {
		if strings.Contains(c, "ghp_secret123") {
			t.Errorf("token on a command line: %s", c)
		}
		if strings.Contains(c, "gh auth login") && login < 0 {
			login = i
		}
		if strings.Contains(c, "bash -s -- '"+origin+"'") && clone < 0 {
			clone = i
		}
	}
	if login < 0 || clone < 0 || login > clone {
		t.Errorf("login at %d, clone at %d", login, clone)
	}
	if !strings.Contains(strings.Join(e.Srv.Calls(), "\n"), "gh auth setup-git") {
		t.Error("git does not use the gh credentials")
	}
	if strings.Contains(e.Cloud.UserData["claude-web"], "ghp_secret123") {
		t.Error("token in the user-data")
	}
}

func TestGhTokenPerMachine(t *testing.T) {
	e := New(t, "")
	e.Machine("web", "VIPER_GH_TOKEN=ghp_web_only\n")
	e.MustUp("web", "")
	if got := strings.TrimSpace(e.Srv.File("gh-token")); got != "ghp_web_only" {
		t.Errorf("machine token: %q", got)
	}
	_ = os.Remove(filepath.Join(e.Srv.State, "gh-token")) // may not exist
	e.MustUp("api", "")
	if e.Srv.Exists("gh-token") {
		t.Error("another machine got the token")
	}
}

func TestNoGhLoginWithoutAToken(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	for _, c := range e.Srv.Calls() {
		if strings.Contains(c, "gh auth") {
			t.Errorf("gh login without a token: %s", c)
		}
	}
}
