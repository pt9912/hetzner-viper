package integration_test

import (
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func (e *Env) probes() int {
	n := 0
	for _, c := range e.Srv.Calls() {
		if strings.Contains(c, "cloud-init status") {
			n++
		}
	}
	return n
}

func (e *Env) state(name string) string {
	b, _ := os.ReadFile(filepath.Join(e.Home, ".local", "state", "hetzner-claude", name))
	return strings.TrimSpace(string(b))
}

func TestCloneWithoutABranchAndWithAnOddPath(t *testing.T) {
	e := New(t, "")
	odd := filepath.Join(t.TempDir(), "my repo;x.git")
	if out, err := gitCmd("", "init", "-q", "--bare", odd); err != nil {
		t.Fatal(out)
	}
	e.MustUp("web", odd)
	if _, err := os.Stat(filepath.Join(e.Config("web").WorkspaceDir, "my repo;x", ".git")); err != nil {
		t.Errorf("not cloned despite an empty branch and metacharacters: %v", err)
	}
}

func TestCloneWithADeployKeyAndNoBranch(t *testing.T) {
	e := New(t, "")
	origin := originRepo(t)
	key := filepath.Join(e.Home, ".ssh", "deploy")
	if err := os.WriteFile(key, []byte("k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.Machine("web", "REPO_URL="+origin+"\nGIT_DEPLOY_KEY_FILE="+key+"\n")
	e.MustUp("web", "")
	if _, err := os.Stat(filepath.Join(e.Config("web").WorkspaceDir, "origin", ".git")); err != nil {
		t.Error(err)
	}
}

func TestScriptsTrustOnlyThePinnedHostKey(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	pub := strings.Join(strings.Fields(e.userData("claude-web")["ssh_keys"].(map[string]any)["ed25519_public"].(string))[:2], " ")
	pin := filepath.Join(e.Home, ".ssh", "known_hosts_hetzner_claude-web")
	if b, _ := os.ReadFile(pin); strings.TrimSpace(string(b)) != "claude-web.example.com "+pub {
		t.Errorf("pin %q, want %q", b, "claude-web.example.com "+pub)
	}
	cfg := e.sshConfigFile("web")
	if !strings.Contains(cfg, "UserKnownHostsFile "+pin+"\n") || !strings.Contains(cfg, "HostKeyAlgorithms ssh-ed25519-cert-v01@openssh.com,ssh-ed25519\n") {
		t.Errorf("ssh config:\n%s", cfg)
	}
}

func TestWithoutAPinTheHostCAIsTrusted(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	_ = os.Remove(filepath.Join(e.Home, ".ssh", "known_hosts_hetzner_claude-web")) // e.g. server created on another computer
	e.MustUp("web", "")
	if !strings.Contains(e.sshConfigFile("web"), "UserKnownHostsFile "+filepath.Join(e.Home, ".ssh", "known_hosts_hetzner_claude_ca")+"\n") {
		t.Errorf("ssh config:\n%s", e.sshConfigFile("web"))
	}
}

func TestExistingPublicKeyIsReusedUnderItsName(t *testing.T) {
	for _, name := range []string{"my-laptop", "my laptop key"} {
		e := New(t, "")
		pub, _ := os.ReadFile(filepath.Join(e.Home, ".ssh", "hetzner-build.pub"))
		e.Cloud.Keys = append(e.Cloud.Keys, hetznerKey(name, string(pub)))
		e.MustUp("web", "")
		if e.changes("ssh-key create") != 0 || e.Cloud.Created[0].SSHKey != name {
			t.Errorf("%q: changes %v, server key %q", name, e.Cloud.Changes(), e.Cloud.Created[0].SSHKey)
		}
	}
}

func TestKeyNameTakenByAnotherPublicKeyFails(t *testing.T) {
	e := New(t, "")
	other := filepath.Join(t.TempDir(), "other")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", other).CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	pub, _ := os.ReadFile(other + ".pub")
	e.Cloud.Keys = append(e.Cloud.Keys, hetznerKey("claude-login", string(pub)))
	r := e.Up("web", "")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "belongs to a different public key")
	if len(e.Cloud.Servers) != 0 {
		t.Error("a server was created")
	}
}

func TestFirewallErrorAbortsBeforeAServerExists(t *testing.T) {
	e := New(t, "")
	e.Cloud.FirewallErr = errors.New("describe failed")
	r := e.Up("web", "")
	if r.Code != 1 || len(e.Cloud.Servers) != 0 {
		t.Errorf("code %d, servers %v", r.Code, e.Cloud.Servers)
	}
	r.Has(t, "Hetzner: listing the firewalls failed")
}

func TestTheSameMachineNameUnderTwoPrefixesDoesNotCollide(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	e.Machine("web", "SERVER_PREFIX=alice\n")
	e.MustUp("web", "")
	if len(e.Cloud.DNSRecords) != 2 {
		t.Errorf("dns %v", e.Cloud.DNSRecords)
	}
	if _, err := os.Stat(filepath.Join(e.Home, ".ssh", "config_hetzner_alice-web")); err != nil {
		t.Error("own config")
	}
	if !strings.Contains(e.sshConfigFile("web"), "claude-web.example.com") {
		t.Error("first config touched")
	}
}

func TestHetznerErrorAbortsStartWithoutCreating(t *testing.T) {
	e := New(t, "")
	e.Cloud.ListErr = errors.New("hcloud: server responded with status code 401")
	r := e.Up("web", "")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "Hetzner API not usable")
	if len(e.Cloud.DNSRecords) != 0 || len(e.Cloud.Changes()) != 0 {
		t.Error("something was created")
	}
}

func TestCloudflareTokenIsNotInArgvOrLogs(t *testing.T) {
	e := New(t, "")
	e.Tokens["CLOUDFLARE_API_TOKEN"] = "cf_secret_42"
	e.MustUp("web", "")
	for _, c := range e.Srv.Calls() {
		if strings.Contains(c, "cf_secret_42") {
			t.Errorf("token in a call: %s", c)
		}
	}
	for _, f := range []string{"events.log", "logs/claude-web.log"} {
		if strings.Contains(e.state(f), "cf_secret_42") {
			t.Errorf("token in %s", f)
		}
	}
}

func TestAnExportedGhTokenIsNotCopiedToTheServer(t *testing.T) {
	e := New(t, "")
	e.Tokens["GH_TOKEN"] = "ghp_my_local_token"
	e.MustUp("web", "")
	for _, c := range e.Srv.Calls() {
		if strings.Contains(c, "gh auth") {
			t.Errorf("gh login: %s", c)
		}
	}
}

func TestHostKeyRejectionIsNamedNotCalledUnreachable(t *testing.T) {
	e := New(t, "")
	e.Srv.Env["MOCK_SSH_HOSTKEY_FAIL"] = "1"
	r := e.Up("web", "")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "host key is not trusted")
	r.HasNot(t, "SSH not reachable")
}

func TestStalePinIsDroppedWhenTheServerWasRecreated(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	pin := filepath.Join(e.Home, ".ssh", "known_hosts_hetzner_claude-web")
	old, _ := os.ReadFile(pin)
	// another computer deletes and recreates claude-web: a new ID, a new host key
	srv := e.Cloud.Servers["claude-web"]
	srv.ID += 100
	e.Srv.SetHostKey("ssh-ed25519 AAAAnewkey claude-web\n")
	r := e.MustUp("web", "")
	r.Has(t, "was recreated")
	got, _ := os.ReadFile(pin)
	if string(got) != "claude-web.example.com ssh-ed25519 AAAAnewkey\n" || string(got) == string(old) {
		t.Errorf("pin %q (old %q)", got, old)
	}
}

func TestMissingPinIsRecordedAfterTheCAVerifiedTheConnection(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	if err := os.Remove(filepath.Join(e.Home, ".ssh", "known_hosts_hetzner_claude-web")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(e.Home, ".local", "state", "hetzner-claude", "id-claude-web")); err != nil {
		t.Fatal(err)
	}
	e.Srv.SetHostKey("ssh-ed25519 AAAAserverkey root@host\n")
	e.MustUp("web", "")
	pin := filepath.Join(e.Home, ".ssh", "known_hosts_hetzner_claude-web")
	if b, _ := os.ReadFile(pin); string(b) != "claude-web.example.com ssh-ed25519 AAAAserverkey\n" {
		t.Errorf("pin %q", b)
	}
	if !strings.Contains(e.sshConfigFile("web"), "UserKnownHostsFile "+pin+"\n") {
		t.Error("the config did not switch to the pin")
	}
}

func TestSymlinkedSshConfigStaysASymlink(t *testing.T) {
	e := New(t, "")
	dots := t.TempDir()
	target := filepath.Join(dots, "ssh_config")
	if err := os.WriteFile(target, []byte("Host other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(e.Home, ".ssh", "config")); err != nil {
		t.Fatal(err)
	}
	e.MustUp("web", "")
	if st, _ := os.Lstat(filepath.Join(e.Home, ".ssh", "config")); st.Mode()&os.ModeSymlink == 0 {
		t.Error("no longer a symlink")
	}
	b, _ := os.ReadFile(target)
	if !strings.Contains(string(b), "Include "+filepath.Join(e.Home, ".ssh", "config_hetzner_claude-web")) || !strings.Contains(string(b), "Host other") {
		t.Errorf("target:\n%s", b)
	}
}

func TestHostKeyCommentIsTheServerNameAndSshConfigIsSafe(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	pub := e.userData("claude-web")["ssh_keys"].(map[string]any)["ed25519_public"].(string)
	if f := strings.Fields(pub); len(f) != 3 || f[2] != "claude-web" {
		t.Errorf("host key comment: %q", pub)
	}
	cfg := e.sshConfigFile("web")
	for _, want := range []string{"    ServerAliveInterval 15\n", "    ForwardAgent no\n", "    ForwardX11 no\n"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("missing %q", want)
		}
	}
}

// a home like Ubuntu's: .bashrc returns early for non-interactive shells
func ubuntuHome(e *Env) {
	if err := os.MkdirAll(e.Srv.Home, 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.Srv.Home, ".bashrc"), []byte("case $- in\n    *i*) ;;\n      *) return;;\nesac\nexport FROM_BASHRC=1\n"), 0o644); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.Srv.Home, ".profile"), []byte("if [ -n \"$BASH_VERSION\" ] && [ -f \"$HOME/.bashrc\" ]; then . \"$HOME/.bashrc\"; fi\n"), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *Env) shell(args ...string) string {
	cmd := exec.Command("bash", args...)
	cmd.Env = []string{"HOME=" + e.Srv.Home, "PATH=" + os.Getenv("PATH")}
	out, _ := cmd.Output()
	return string(out)
}

func TestClaudeTokenReachesNonInteractiveAndLoginShells(t *testing.T) {
	e := New(t, "")
	ubuntuHome(e)
	token := `sk-ant-oat01-a$b"c'd`
	e.Tokens["VIPER_CLAUDE_TOKEN"] = token
	e.MustUp("web", "")
	if m := mode(t, filepath.Join(e.Srv.Home, ".config/viper/claude.env")); m != 0o600 {
		t.Errorf("mode %v", m)
	}
	if got := e.shell("-c", `. ~/.bashrc; printf %s "$CLAUDE_CODE_OAUTH_TOKEN"`); got != token {
		t.Errorf("non-interactive ssh shell: %q", got)
	}
	if got := e.shell("-lc", `printf %s "$CLAUDE_CODE_OAUTH_TOKEN"`); got != token {
		t.Errorf("login shell: %q", got)
	}
	if !strings.Contains(e.ServerFile(".bashrc"), "export FROM_BASHRC=1") {
		t.Error("the rest of .bashrc was lost")
	}
	for _, c := range e.Srv.Calls() {
		if strings.Contains(c, "sk-ant-oat01") {
			t.Errorf("token on a command line: %s", c)
		}
	}
	if strings.Contains(e.Cloud.UserData["claude-web"], "sk-ant-oat01") {
		t.Error("token in the user-data")
	}
}

func TestClaudeTokenRerunDoesNotDuplicateAndUpdates(t *testing.T) {
	e := New(t, "")
	ubuntuHome(e)
	e.Tokens["VIPER_CLAUDE_TOKEN"] = "first"
	e.MustUp("web", "")
	e.Tokens["VIPER_CLAUDE_TOKEN"] = "second"
	e.MustUp("web", "")
	for _, f := range []string{".bashrc", ".profile"} {
		if n := countLines(e.ServerFile(f), "viper/claude.env"); n != 1 {
			t.Errorf("%s has the loader line %d times", f, n)
		}
	}
	if got := e.shell("-c", `. ~/.bashrc; printf %s "$CLAUDE_CODE_OAUTH_TOKEN"`); got != "second" {
		t.Errorf("new token: %q", got)
	}
}

func TestLocallyExportedClaudeTokenIsNotCopied(t *testing.T) {
	e := New(t, "")
	e.Tokens["CLAUDE_CODE_OAUTH_TOKEN"] = "sk-ant-oat01-local"
	e.MustUp("web", "")
	if _, err := os.Stat(filepath.Join(e.Srv.Home, ".config/viper/claude.env")); err == nil {
		t.Error("a login was set up")
	}
}

func TestClaudeTokenWorksWithoutAnExistingBashrcAndProfile(t *testing.T) {
	e := New(t, "")
	e.Tokens["VIPER_CLAUDE_TOKEN"] = "sk-ant-oat01-x"
	e.MustUp("web", "")
	if !strings.Contains(e.ServerFile(".bashrc"), "viper/claude.env") {
		t.Error("loader not created")
	}
	if got := e.shell("-c", `. ~/.bashrc; printf %s "$CLAUDE_CODE_OAUTH_TOKEN"`); got != "sk-ant-oat01-x" {
		t.Errorf("token: %q", got)
	}
}

func TestUnwritableSshConfigOnlyWarns(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can write anything")
	}
	e := New(t, "")
	dots := t.TempDir()
	target := filepath.Join(dots, "ssh_config")
	if err := os.WriteFile(target, []byte("Host other\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(e.Home, ".ssh", "config")); err != nil {
		t.Fatal(err)
	}
	e.Preflight("web", "").Has(t, "WARN  ~/.ssh/config (")
	r := e.MustUp("web", "")
	r.Has(t, "Ready: ssh hetzner-claude-web", "WARNING: could not edit ~/.ssh/config")
}

func TestSshRepoUrlWithoutADeployKeyFailsThePreflight(t *testing.T) {
	e := New(t, "")
	r := e.Up("web", "git@github.com:o/r.git")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "needs GIT_DEPLOY_KEY_FILE")
	if len(e.Cloud.Servers) != 0 {
		t.Error("a server was created")
	}
}

func TestSshRepoUrlOfTheGhHostGoesOverHttpsWithTheToken(t *testing.T) {
	e := New(t, "")
	e.Tokens["VIPER_GH_TOKEN"] = "ghp_x"
	e.Preflight("web", "git@github.com:o/r.git").Has(t, "will be cloned over HTTPS with VIPER_GH_TOKEN")
	r := e.Preflight("web", "git@gitlab.com:o/r.git")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "needs GIT_DEPLOY_KEY_FILE")
	e.MustUp("web", "")
	all := strings.Join(e.Srv.Calls(), "\n")
	if !strings.Contains(all, "insteadOf 'git@github.com:'") || !strings.Contains(all, "insteadOf 'ssh://git@github.com/'") {
		t.Errorf("git does not rewrite SSH URLs to HTTPS:\n%s", all)
	}
}

func TestRepoUrlStartingWithADashIsRejected(t *testing.T) {
	e := New(t, "")
	r := e.Up("web", "--upload-pack=touch /tmp/x")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "REPO_URL must not start with '-'")
	if len(e.Cloud.Servers) != 0 {
		t.Error("a server was created")
	}
}

func TestReusedServerWithAnUntrustedKeyFailsFast(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	e.Srv.Env["MOCK_SSH_HOSTKEY_FAIL"] = "1"
	r := e.Up("web", "")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "host key is not trusted")
	delete(e.Srv.Env, "MOCK_SSH_HOSTKEY_FAIL")
	e.Srv.Env["MOCK_SSH_AUTH_FAIL"] = "1"
	sleeps := len(e.Sleeps)
	r = e.Up("web", "")
	r.Has(t, "rejects the login key")
	if len(e.Sleeps) != sleeps {
		t.Errorf("a long retry: %d sleeps", len(e.Sleeps)-sleeps)
	}
}

func TestClassicTokenWithABroaderOrgScopeIsAccepted(t *testing.T) {
	e := New(t, "")
	e.Tokens["VIPER_GH_TOKEN"] = "ghp_x"
	e.GitHub("repo, admin:org")
	e.Preflight("web", "").HasNot(t, "lacks scope")
}

func TestANewServerWaitsForTheRebootAfterUpdates(t *testing.T) {
	e := New(t, "")
	e.Srv.Write("reboot-required", "")
	r := e.MustUp("web", "")
	r.Has(t, "Updates need a reboot; waiting for the server to restart", "Ready: ssh hetzner-claude-web")
	if e.probes() != 2 {
		t.Errorf("%d probes", e.probes())
	}
}

func TestReusedServerNeedingARebootOnlyWarns(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	e.Srv.Write("reboot-required", "")
	e.Srv.Write("reboot-required.keep", "") // e.g. after unattended upgrades
	n := e.probes()
	r := e.MustUp("web", "")
	if e.probes()-n != 1 {
		t.Errorf("waited: %d probes", e.probes()-n)
	}
	r.Has(t, "WARNING: updates on the server need a reboot: viper ssh web --cmd 'sudo reboot'")
	e.Check("web").Has(t, "WARN  updates need a reboot")
}

func TestCloudInitUpgradesAndRebootsOnlyIfRequired(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	d := e.userData("claude-web")
	if d["package_upgrade"] != true {
		t.Errorf("package_upgrade %v", d["package_upgrade"])
	}
	p, _ := d["power_state"].(map[string]any)
	if p["mode"] != "reboot" || p["condition"] != "test -f /var/run/reboot-required" {
		t.Errorf("power_state %v", p)
	}
	rc := list(d["runcmd"])
	if rc[len(rc)-1] != "touch /etc/cloud/cloud-init.disabled" {
		t.Errorf("last runcmd %v", rc[len(rc)-1])
	}
}

func TestCloudInitWritesATmuxConfigForBuilder(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	var found map[string]any
	for _, w := range list(e.userData("claude-web")["write_files"]) {
		if m := w.(map[string]any); m["path"] == "/home/builder/.tmux.conf" {
			found = m
		}
	}
	if found == nil {
		t.Fatal("no tmux config")
	}
	// before the user exists write_files could not chown: it must be deferred
	if found["defer"] != true || found["owner"] != "builder:builder" {
		t.Errorf("%v", found)
	}
	c := found["content"].(string)
	if !strings.Contains(c, "set -g mouse on") || !strings.Contains(c, "bind -n M-1 select-window -t 1") {
		t.Errorf("content:\n%s", c)
	}
}

func TestDockerComesFromDockersRepositoryWithBuildxAndCompose(t *testing.T) {
	e := New(t, "")
	r := e.MustUp("web", "")
	d := e.userData("claude-web")
	for _, p := range list(d["packages"]) {
		if p == "docker.io" {
			t.Error("Ubuntu's docker.io is installed")
		}
	}
	var docker []string
	rc := list(d["runcmd"])
	for _, c := range rc {
		if s, ok := c.(string); ok && strings.Contains(s, "download.docker.com") {
			docker = append(docker, s)
		}
	}
	if len(docker) != 1 {
		t.Fatalf("docker commands: %v", docker)
	}
	for _, want := range []string{"docker-buildx-plugin", "docker-compose-plugin", "docker-ce ", "9DC858229FC7DD38854AE2D88D81803C0EBFCD88"} {
		if !strings.Contains(docker[0], want) {
			t.Errorf("missing %q", want)
		}
	}
	// set -e must stay inside the subshell, or a failure would skip the rest of runcmd
	if !strings.HasPrefix(strings.TrimSpace(docker[0]), "(") {
		t.Error("not in a subshell")
	}
	idx := func(want string) int {
		for i, c := range rc {
			if c == want {
				return i
			}
		}
		return -1
	}
	enable := idx("systemctl enable --now docker")
	if enable < 0 {
		t.Fatal("docker is not enabled")
	}
	for i, c := range rc {
		if s, ok := c.(string); ok && s == docker[0] && i > enable {
			t.Error("docker is enabled before it is installed")
		}
	}
	// the inventory asks for Docker's packages
	all := strings.Join(e.Srv.Calls(), "\n")
	if !strings.Contains(all, "'docker-ce' 'docker-ce-cli' 'containerd.io' 'docker-buildx-plugin' 'docker-compose-plugin'") {
		t.Errorf("inventory:\n%s", all)
	}
	_ = r
}

func TestInteractiveShellsStartInTheWorkDirectory(t *testing.T) {
	e := New(t, "")
	ubuntuHome(e)
	origin := originRepo(t)
	e.MustUp("web", origin)
	wd := filepath.Join(e.Config("web").WorkspaceDir, "origin")
	if got := strings.TrimSpace(e.ServerFile(".config/viper/workdir")); got != wd {
		t.Errorf("work dir recorded: %q", got)
	}
	pwd := func(dir string, args ...string) string {
		cmd := exec.Command("bash", args...)
		cmd.Dir = dir
		cmd.Env = []string{"HOME=" + e.Srv.Home, "PATH=" + os.Getenv("PATH")}
		cmd.Stdin = nil
		out, _ := cmd.Output()
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		return lines[len(lines)-1]
	}
	if got := pwd(e.Srv.Home, "-i", "-c", "pwd"); got != wd {
		t.Errorf("interactive shell from home: %q", got)
	}
	if got := pwd(e.Srv.Home, "-c", ". ~/.bashrc; pwd"); got != e.Srv.Home {
		t.Errorf("non-interactive shell stays: %q", got)
	}
	if got := pwd("/tmp", "-i", "-c", "pwd"); got != "/tmp" {
		t.Errorf("elsewhere stays elsewhere: %q", got)
	}
	e.MustUp("web", origin)
	if n := strings.Count(e.ServerFile(".bashrc"), "viper: start in the work directory"); n != 1 {
		t.Errorf("line added %d times", n)
	}
}

func TestAnInterruptedFirstRunIsNotASetUpServer(t *testing.T) {
	e := New(t, "")
	e.Cloud.DNSFail = true // the server is created, the run aborts at DNS
	if r := e.Up("web", ""); r.Code != 1 {
		t.Fatalf("code %d", r.Code)
	}
	if e.Cloud.Servers["claude-web"] == nil {
		t.Fatal("no server")
	}
	if e.state("ready-claude-web") != "" {
		t.Error("marked ready")
	}
	e.Cloud.DNSFail = false
	e.Srv.Write("reboot-required", "") // its reboot is still to come
	r := e.MustUp("web", "")
	r.Has(t, "Updates need a reboot; waiting")
	if e.state("ready-claude-web") != e.state("id-claude-web") || e.state("ready-claude-web") == "" {
		t.Errorf("ready %q, id %q", e.state("ready-claude-web"), e.state("id-claude-web"))
	}
}

func TestTransientHostKeyErrorOfABootingServerIsRetried(t *testing.T) {
	e := New(t, "")
	e.Cloud.DNSFail = true
	e.Up("web", "")
	e.Cloud.DNSFail = false
	e.Srv.Env["MOCK_SSH_HOSTKEY_FAIL"] = "1"
	before := len(e.Sleeps)
	r := e.Up("web", "")
	if r.Code != 1 || len(e.Sleeps)-before < 100 {
		t.Errorf("code %d, %d pauses: a booting server is retried, not aborted at once", r.Code, len(e.Sleeps)-before)
	}
}

func TestUnreachableErrorShowsTheLastSshError(t *testing.T) {
	e := New(t, "")
	e.Srv.Env["MOCK_SSH_DOWN"] = "1"
	e.Up("web", "").Has(t, "SSH not reachable; server retained. Last error: ssh: connect to host")
}

func TestWorkspaceIsRecordedOnTheServerOnce(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	e.MustUp("web", "")
	if got := strings.TrimSpace(e.ServerFile(".config/viper/workspaces")); got != e.Config("web").WorkspaceDir {
		t.Errorf("recorded %q", got)
	}
}

func TestDeployKeyIsNotBypassedByTheGithubToken(t *testing.T) {
	e := New(t, "")
	key := filepath.Join(e.Home, ".ssh", "deploy")
	if err := os.WriteFile(key, []byte("k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.Machine("web", "GIT_DEPLOY_KEY_FILE="+key+"\nVIPER_GH_TOKEN=ghp_x\n")
	e.HTTP = func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "api.github.com" && strings.HasPrefix(r.URL.Path, "/repos/") {
			return Reply(404, `{}`, nil), nil
		}
		return defaultHTTP(r)
	}
	e.Preflight("web", "git@github.com:o/r.git").HasNot(t, "token cannot access")
	e.Up("web", "git@github.com:o/r.git") // the clone itself cannot work here
	all := strings.Join(e.Srv.Calls(), "\n")
	if !strings.Contains(all, "unset-all url.'https://github.com/'.insteadOf") {
		t.Errorf("old rewrite not removed:\n%s", all)
	}
	if strings.Contains(all, "--add url.'https://github.com/'.insteadOf") {
		t.Error("a new rewrite was added")
	}
}

func TestIpadPrivateKeyIsRejected(t *testing.T) {
	e := New(t, "")
	e.Machine("web", "IPAD_SSH_PUBLIC_KEY_FILE="+filepath.Join(e.Home, ".ssh", "hetzner-build")+"\n") // the private one
	r := e.Up("web", "")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "must be a public key")
	if len(e.Cloud.Servers) != 0 {
		t.Error("a server was created")
	}
}

func TestHomebrewIsInstalledAndOnThePath(t *testing.T) {
	e := New(t, "")
	ubuntuHome(e)
	e.MustUp("web", "")
	all := strings.Join(e.Srv.Calls(), "\n")
	if !strings.Contains(all, "raw.githubusercontent.com/Homebrew/install/HEAD/install.sh") || !strings.Contains(all, "NONINTERACTIVE=1") {
		t.Errorf("installer:\n%s", all)
	}
	if got := e.shell("-c", `. ~/.bashrc; command -v brew`); !strings.Contains(got, "linuxbrew") {
		t.Errorf("brew in non-interactive shells: %q", got)
	}
	if got := e.shell("-lc", `command -v brew`); !strings.Contains(got, "linuxbrew") {
		t.Errorf("brew in login shells: %q", got)
	}
	e.MustUp("web", "")
	if n := countLines(e.ServerFile(".bashrc"), "shellenv"); n != 1 {
		t.Errorf("path line %d times", n)
	}
}

func TestHomebrewCanBeSkipped(t *testing.T) {
	e := New(t, "")
	e.Machine("web", "VIPER_HOMEBREW=0\n")
	e.MustUp("web", "")
	for _, c := range e.Srv.Calls() {
		if strings.Contains(c, "Homebrew/install") {
			t.Errorf("installer: %s", c)
		}
	}
	if r := e.Check("web"); strings.Contains(r.Out, "Homebrew") {
		t.Errorf("check expects it:\n%s", r.Out)
	}
}

func TestFailedHomebrewInstallOnlyWarns(t *testing.T) {
	e := New(t, "")
	e.Srv.Env["MOCK_BREW_FAIL"] = "1"
	r := e.Up("web", "")
	if r.Code != 0 {
		t.Fatalf("%d\n%s", r.Code, r.Out)
	}
	r.Has(t, "WARNING: Homebrew installation failed", "Ready: ssh hetzner-claude-web")
}

func TestReadyMarkerOnTheServerCountsForAnotherComputer(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	if got := strings.TrimSpace(e.ServerFile(".config/viper/ready")); got != e.state("id-claude-web") || got == "" {
		t.Fatalf("server marker %q, id %q", got, e.state("id-claude-web"))
	}
	_ = os.Remove(filepath.Join(e.Home, ".local", "state", "hetzner-claude", "ready-claude-web")) // like a second computer
	e.Srv.Write("reboot-required", "")
	e.Srv.Write("reboot-required.keep", "")
	n := e.probes()
	r := e.MustUp("web", "")
	if e.probes()-n != 1 {
		t.Errorf("waited: %d probes", e.probes()-n)
	}
	r.Has(t, "updates on the server need a reboot")
}

func TestWaitForAPendingRebootIsCapped(t *testing.T) {
	e := New(t, "")
	e.Cloud.DNSFail = true
	e.Up("web", "") // interrupted first run, no marker
	e.Cloud.DNSFail = false
	e.Srv.Write("reboot-required", "")
	e.Srv.Write("reboot-required.keep", "") // the reboot never comes
	n := e.probes()
	r := e.MustUp("web", "")
	if e.probes()-n != 37 {
		t.Errorf("%d probes, want 37", e.probes()-n)
	}
	r.Has(t, "Ready: ssh hetzner-claude-web", "updates on the server need a reboot")
}

func TestWorkspaceIsRecordedOnTheServerBeforeLaterSteps(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	found := false
	for _, c := range e.Srv.Calls() {
		if strings.Contains(c, "sudo install -d -o builder -g builder") && strings.Contains(c, "~/.config/viper/workspaces") {
			found = true
		}
	}
	if !found {
		t.Error("not recorded with the creation of the work directory")
	}
}

func TestRepoGivenOnceIsRemembered(t *testing.T) {
	e := New(t, "")
	origin := originRepo(t)
	e.MustUp("web", origin)
	r := e.MustUp("web", "") // e.g. to refresh a token, without a repository argument
	r.Has(t, "Repository (remembered from an earlier start)")
	wd := filepath.Join(e.Config("web").WorkspaceDir, "origin")
	if got := strings.TrimSpace(e.ServerFile(".config/viper/workdir")); got != wd {
		t.Errorf("work dir kept: %q", got)
	}
	e.Check("web").Has(t, "ok    repository "+wd+" at ")
	// its remote is a local path: DISCARD
	e.Down("web", false, "DISCARD", "DELETE")
	if e.state("repo-claude-web") != "" {
		t.Error("not forgotten with the machine")
	}
}

func TestWorkspaceRecordedOnTheServerSurvivesALaterFailure(t *testing.T) {
	e := New(t, "")
	e.Machine("web", "VIPER_GH_TOKEN=ghp_x\n")
	e.Srv.Env["MOCK_GH_LOGIN_FAIL"] = "1"
	if r := e.Up("web", ""); r.Code != 1 {
		t.Fatalf("code %d", r.Code)
	}
	if got := strings.TrimSpace(e.ServerFile(".config/viper/workspaces")); got != e.Config("web").WorkspaceDir {
		t.Errorf("recorded %q", got)
	}
}

func TestHomebrewInstallerRunsWhenBrewIsMissing(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	_ = os.Remove(e.Srv.BrewPath) // a fresh server has no brew
	e.MustUp("web", "")
	if _, err := os.Stat(e.Srv.BrewPath); err != nil {
		t.Errorf("not installed by the (fake) installer: %v", err)
	}
	if !strings.Contains(e.Log("claude-web"), "homebrew: Homebrew ") {
		t.Error("not reported in the log")
	}
}

func TestOldServerCountsAsSetUpOnAnotherComputer(t *testing.T) {
	e := New(t, "")
	e.MustUp("web", "")
	if err := os.Remove(filepath.Join(e.Home, ".local", "state", "hetzner-claude", "ready-claude-web")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(e.Srv.Home, ".config/viper/ready")); err != nil {
		t.Fatal(err)
	}
	e.Cloud.Servers["claude-web"].Created = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e.Srv.Env["MOCK_SSH_HOSTKEY_FAIL"] = "1"
	sleeps := len(e.Sleeps)
	r := e.Up("web", "")
	if r.Code != 1 || len(e.Sleeps) != sleeps {
		t.Errorf("code %d, %d pauses: must fail fast", r.Code, len(e.Sleeps)-sleeps)
	}
	r.Has(t, "host key is not trusted")
}

// countLines is the number of lines that contain s.
func countLines(text, s string) int {
	n := 0
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, s) {
			n++
		}
	}
	return n
}
