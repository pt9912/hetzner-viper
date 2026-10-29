package start

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	hetznerviper "github.com/pt9912/hetzner-viper"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
	"github.com/pt9912/hetzner-viper/internal/usecase/preflight"
)

// provision sets the server up once ssh works: the steps after the wait.
func (r *runner) provision(ctx context.Context, status, id, repoURL string, remembered bool, deployKey string) (int, error) {
	if code, err := r.baseSystem(ctx, status); code != 0 || err != nil {
		return code, err
	}
	if code, err := r.githubLogin(ctx, deployKey); code != 0 || err != nil {
		return code, err
	}
	if interrupted(ctx) {
		return 1, driving.ErrShown
	}
	r.gitIdentity(ctx)
	if code, err := r.claudeLogin(ctx); code != 0 || err != nil {
		return code, err
	}
	workdir, code, err := r.repository(ctx, repoURL, remembered, deployKey)
	if code != 0 || err != nil {
		return code, err
	}
	if interrupted(ctx) {
		return 1, driving.ErrShown
	}
	if code, err := r.tools(ctx, repoURL, workdir); code != 0 || err != nil {
		return code, err
	}
	return r.finish(ctx, id, workdir)
}

// baseSystem checks the new server and sets up its swap file and work directory.
func (r *runner) baseSystem(ctx context.Context, status string) (int, error) {
	if code, err := r.verifyStatus(ctx, status); code != 0 || err != nil {
		return code, err
	}
	r.swap(ctx)
	if interrupted(ctx) {
		return 1, driving.ErrShown
	}
	return r.workspace(ctx)
}

// tools installs what the work needs after the clone: Homebrew, then the VIPER_POST_CLONE commands
// (which can use the tools), then code-server.
func (r *runner) tools(ctx context.Context, repoURL, workdir string) (int, error) {
	r.homebrew(ctx, repoURL, workdir)
	if interrupted(ctx) {
		return 1, driving.ErrShown
	}
	if code, err := r.postClone(ctx, repoURL, workdir); code != 0 || err != nil {
		return code, err
	}
	r.codeServer(ctx)
	if interrupted(ctx) {
		return 1, driving.ErrShown
	}
	return 0, nil
}

// verifyStatus checks the readiness probe's report and repairs a failed Claude install.
func (r *runner) verifyStatus(ctx context.Context, status string) (int, error) {
	c := r.c
	// runcmd runs only once per server; a failed Claude install is repaired here instead.
	if !hasLine(status, "claude=ok") {
		if code, err := r.installClaude(ctx); code != 0 || err != nil {
			return code, err
		}
	}
	if !hasLine(status, "docker=ok") {
		return r.fail("Docker is not running; see /var/log/cloud-init-output.log on the server. Server retained.")
	}
	if hasLine(status, "cloud-init=1") {
		r.eprintf("WARNING: cloud-init reported errors; see /var/log/cloud-init-output.log on the server.\n")
	}
	if hasLine(status, "reboot=pending") {
		r.eprintf("WARNING: updates on the server need a reboot: viper ssh %s --cmd 'sudo reboot'\n", c.Name)
	}
	return 0, nil
}

// swap sets the swap file up (not fatal).
func (r *runner) swap(ctx context.Context) {
	rerun := "viper up " + r.c.Name
	// Swap file, opt-in with VIPER_SWAP (not fatal): a factor of the server's RAM (1 = as much as
	// RAM, 0.5 = half) or a size (4G, 512M). Idempotent: an active /swapfile is left alone.
	if swap := r.c.Setting("VIPER_SWAP", "0"); swap != "0" {
		if err := r.script(ctx, swapScript, swap); err != nil {
			r.warnf(ctx, "WARNING: swap could not be set up; rerun %s\n", rerun)
		}
	}
}

// finish makes the shells change to the work directory and marks the server as set up.
func (r *runner) finish(ctx context.Context, id, workdir string) (int, error) {
	c := r.c
	// Interactive shells (viper ssh, tmux, a plain ssh login) that start in the home directory
	// change to the work directory: the line goes at the end of .bashrc, which non-interactive
	// shells (ssh commands, the scripts) never reach because of Ubuntu's early return.
	brew := c.Setting("VIPER_BREW", "/home/linuxbrew/.linuxbrew/bin/brew")
	if err := r.script(ctx, cdLineScript, workdir, c.WorkspaceDir, brew, id); err != nil {
		return r.fail("setting up the shells on the server failed")
	}
	// from now on this server counts as set up (see reused)
	_ = os.WriteFile(c.ReadyState, []byte(id+"\n"), 0o600)
	r.inventory(ctx, brew)
	r.lg.Final("ready (work dir %s)", workdir)
	r.printf("Ready: ssh %s (work dir: %s)\n", c.SSHAlias, workdir)
	if r.d.OpenVSCode != nil {
		r.d.OpenVSCode(c.SSHAlias, workdir)
	}
	return 0, nil
}

func (r *runner) installClaude(ctx context.Context) (int, error) {
	r.printf("Claude Code is not installed yet; installing ...\n")
	// the installer is rate limited per address (HTTP 429, e.g. for a recycled IP): retry with growing pauses
	for attempt := 1; attempt <= 5; attempt++ {
		if ctx.Err() != nil {
			return 1, driving.ErrShown
		}
		if err := r.ssh(ctx, "", "bash -o pipefail -c 'curl -fsSL https://claude.ai/install.sh | bash' && sudo touch /var/tmp/claude-ready"); err == nil {
			return 0, nil
		}
		if interrupted(ctx) {
			return 1, driving.ErrShown
		}
		if attempt != 5 {
			r.eprintf("Claude Code installation failed (attempt %d of 5); retrying in %d s ...\n", attempt, attempt*30)
			r.d.Sleep(time.Duration(attempt*30) * time.Second)
		}
	}
	return r.fail("Claude Code installation failed; server retained. Rerun viper up %s to try again (a rate limit, HTTP 429, passes after a few minutes).", r.c.Name)
}

// workspace creates the work directory on the server's own disk (deleted together with the server)
// and records it, here and on the server right away (also if a later step fails), so that down
// from any computer checks it.
func (r *runner) workspace(ctx context.Context) (int, error) {
	c := r.c
	q := model.Quote(c.WorkspaceDir)
	cmd := "sudo install -d -o builder -g builder " + q + " && mkdir -p ~/.config/viper && { grep -qxF -- " + q +
		" ~/.config/viper/workspaces 2>/dev/null || printf '%s\\n' " + q + " >> ~/.config/viper/workspaces; }"
	if err := r.ssh(ctx, "", cmd); err != nil {
		return r.fail("creating the work directory %s on the server failed", c.WorkspaceDir)
	}
	// every workspace this server was started with: down checks all of them
	for _, l := range strings.Split(readTrim(c.WorkspaceState), "\n") {
		if l == c.WorkspaceDir {
			return 0, nil
		}
	}
	f, err := os.OpenFile(c.WorkspaceState, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 1, err
	}
	defer func() { _ = f.Close() }()
	_, err = fmt.Fprintln(f, c.WorkspaceDir)
	return 0, err
}

// githubLogin logs gh in for builder. The token goes through stdin (not argv, not user-data);
// `gh auth setup-git` makes git use it for HTTPS clone/push. A dedicated variable, so a GH_TOKEN
// the user exported for local gh is never copied to a server.
func (r *runner) githubLogin(ctx context.Context, deployKey string) (int, error) {
	c := r.c
	token := c.Get("VIPER_GH_TOKEN")
	if token == "" {
		return 0, nil
	}
	host := c.Setting("VIPER_GH_HOST", "github.com")
	ghHost := model.Quote(host)
	// Without a deploy key the server has no SSH key for the git host, so SSH URLs of that host
	// go over HTTPS with the token. With a deploy key, SSH (and the key) stay in charge.
	https, scpForm, sshForm := model.Quote("https://"+host+"/"), model.Quote("git@"+host+":"), model.Quote("ssh://git@"+host+"/")
	rewrite := "git config --global --add url." + https + ".insteadOf " + scpForm + " && git config --global --add url." + https + ".insteadOf " + sshForm
	if deployKey != "" {
		rewrite = "true"
	}
	cmd := "gh auth login --hostname " + ghHost + " --with-token && gh auth setup-git --hostname " + ghHost + " &&\n    { git config --global --unset-all url." +
		https + ".insteadOf || true; } && " + rewrite
	if err := r.ssh(ctx, token+"\n", cmd); err != nil {
		return r.fail("gh login on the server failed")
	}
	if deployKey != "" {
		r.printf("gh: logged in to %s (repository via the deploy key)\n", host)
	} else {
		r.printf("gh: logged in to %s (SSH URLs of %s are cloned over HTTPS)\n", host, host)
	}
	return 0, nil
}

// gitIdentity sets the git identity for commits on the server (the agent commits and pushes).
// VIPER_GIT_USER_NAME / VIPER_GIT_USER_EMAIL win; what is missing is taken from the GitHub login.
func (r *runner) gitIdentity(ctx context.Context) {
	c := r.c
	useGH := ""
	if c.Get("VIPER_GH_TOKEN") != "" {
		useGH = "1"
	}
	if err := r.script(ctx, gitIDScript, c.Get("VIPER_GIT_USER_NAME"), c.Get("VIPER_GIT_USER_EMAIL"), c.Setting("VIPER_GH_HOST", "github.com"), useGH); err != nil {
		r.warnf(ctx, "WARNING: git user.name/user.email could not be set on the server\n")
	}
}

// claudeLogin gives builder the Claude Code login, one of:
//   - VIPER_CLAUDE_TOKEN: long-lived subscription token (`claude setup-token`)
//   - VIPER_ANTHROPIC_BASE_URL + VIPER_ANTHROPIC_AUTH_TOKEN + VIPER_CLAUDE_MODEL: another model
//     behind an Anthropic-compatible API; aliases, background tasks and subagents use it too
//
// The settings go through stdin into a 600 file; a line at the very top of .bashrc/.profile
// (before the "not interactive: return" guard) loads it for ssh commands, tmux and VS Code.
func (r *runner) claudeLogin(ctx context.Context) (int, error) {
	c := r.c
	var env, msg string
	if base := c.Get("VIPER_ANTHROPIC_BASE_URL"); base != "" {
		claudeModel := c.Get("VIPER_CLAUDE_MODEL")
		env = "export ANTHROPIC_BASE_URL=" + model.Quote(base) + "\nexport ANTHROPIC_AUTH_TOKEN=" + model.Quote(c.Get("VIPER_ANTHROPIC_AUTH_TOKEN")) +
			"\nexport ANTHROPIC_API_KEY=\"\"\nunset CLAUDE_CODE_OAUTH_TOKEN"
		for _, v := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL"} {
			env += "\nexport " + v + "=" + model.Quote(claudeModel)
		}
		if n := c.Get("VIPER_CLAUDE_MAX_CONTEXT_TOKENS"); n != "" {
			env += "\nexport CLAUDE_CODE_MAX_CONTEXT_TOKENS=" + model.Quote(n)
		}
		msg = "claude: using model " + claudeModel + " at " + preflight.RedactURL(base)
	} else if token := c.Get("VIPER_CLAUDE_TOKEN"); token != "" {
		env = "export CLAUDE_CODE_OAUTH_TOKEN=" + model.Quote(token)
		msg = "claude: logged in with VIPER_CLAUDE_TOKEN"
	}
	if env == "" {
		return 0, nil
	}
	login := `umask 077 && mkdir -p ~/.config/viper && cat > ~/.config/viper/claude.env &&
    line="[ -f ~/.config/viper/claude.env ] && . ~/.config/viper/claude.env" &&
    for f in ~/.bashrc ~/.profile; do
      grep -qxF "$line" "$f" 2>/dev/null || { { echo "$line"; [ ! -f "$f" ] || cat "$f"; } > "$f.viper" && mv "$f.viper" "$f"; }
    done`
	if err := r.ssh(ctx, env+"\n", login); err != nil {
		return r.fail("setting up the Claude Code login on the server failed")
	}
	r.printf("%s\n", msg)
	return 0, nil
}

// repository clones the repository (once) and returns the work directory.
func (r *runner) repository(ctx context.Context, repoURL string, remembered bool, deployKey string) (string, int, error) {
	c := r.c
	workdir := c.WorkspaceDir
	if repoURL == "" {
		return workdir, 0, nil
	}
	// remembered for later runs of start/check (e.g. refreshing a token without a repository argument)
	if remembered {
		r.printf("Repository (remembered from an earlier start): %s\n", preflight.RedactURL(repoURL))
	}
	if err := os.WriteFile(c.RepoState, []byte(repoURL+"\n"), 0o600); err != nil {
		return "", 1, err
	}
	workdir = c.WorkDir(repoURL)
	useKey := ""
	if deployKey != "" {
		useKey = "1"
		if err := r.d.Stream(ctx, "", r.out, r.errOut, "scp", "-F", c.SSHConfig, "-q", deployKey, c.SSHAlias+":.ssh/deploy_key"); err != nil {
			code, err := r.fail("copying the deploy key to the server failed")
			return "", code, err
		}
		if err := r.ssh(ctx, "", "chmod 600 ~/.ssh/deploy_key"); err != nil {
			code, err := r.fail("setting the permissions of the deploy key failed")
			return "", code, err
		}
	}
	// the marker for VIPER_POST_CLONE is only set by a clone made while the commands are configured
	postClone := ""
	if strings.TrimSpace(c.Get("VIPER_POST_CLONE")) != "" {
		postClone = "1"
	}
	if err := r.script(ctx, cloneScript, repoURL, workdir, c.Get("REPO_BRANCH"), useKey, postClone); err != nil {
		code, err := r.fail("cloning the repository failed")
		return "", code, err
	}
	return workdir, 0, nil
}

// homebrew installs Homebrew (official installer, as builder; skipped if already there), then
// taps, trust entries and formulae. Not fatal: a failure is reported, and check shows it.
func (r *runner) homebrew(ctx context.Context, repoURL, workdir string) {
	c := r.c
	if c.Setting("VIPER_HOMEBREW", "1") != "1" {
		return
	}
	brew := c.Setting("VIPER_BREW", "/home/linuxbrew/.linuxbrew/bin/brew")
	rerun := "viper up " + c.Name
	r.printf("Homebrew: installing (skipped if present) ...\n")
	install := "test -x " + brew + " || { mkdir -p ~/.config/viper && f=$(mktemp) &&\n" +
		"      curl -fsSL -o \"$f\" https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh &&\n" +
		"      NONINTERACTIVE=1 bash \"$f\" </dev/null >~/.config/viper/homebrew-install.log 2>&1; rc=$?; rm -f \"$f\";\n" +
		"      [ $rc = 0 ] || tail -n 15 ~/.config/viper/homebrew-install.log; exit $rc; }"
	if err := r.ssh(ctx, "", install); err != nil {
		r.warnf(ctx, "WARNING: Homebrew installation failed (log on the server: ~/.config/viper/homebrew-install.log); rerun %s\n", rerun)
	}
	// Homebrew, in this order: VIPER_BREW_TAPS ("owner/repo", or "owner/repo=URL" for a tap outside
	// GitHub), VIPER_BREW_TRUST (brew trust), VIPER_BREW_PACKAGES and a Brewfile in the cloned
	// repository. The Brewfile is Ruby from the repository: only with VIPER_BREWFILE=1.
	brewfile := ""
	if c.Setting("VIPER_BREWFILE", "0") == "1" && repoURL != "" {
		brewfile = workdir + "/Brewfile"
	}
	taps, trust, pkgs := strings.Fields(c.Get("VIPER_BREW_TAPS")), strings.Fields(c.Get("VIPER_BREW_TRUST")), strings.Fields(c.Get("VIPER_BREW_PACKAGES"))
	if len(taps)+len(trust)+len(pkgs) == 0 && brewfile == "" {
		return
	}
	args := append([]string{brew, brewfile, fmt.Sprint(len(taps)), fmt.Sprint(len(trust))}, taps...)
	args = append(append(args, trust...), pkgs...)
	if err := r.script(ctx, brewPkgScript, args...); err != nil {
		r.warnf(ctx, "WARNING: some Homebrew taps, trust entries or formulae failed (log on the server: ~/.config/viper/brew-packages.log); rerun %s\n", rerun)
	}
}

// postClone runs the shell commands of VIPER_POST_CLONE in the cloned repository (as builder, in
// a login shell, after Homebrew so its tools are there), only right after the clone. When they
// fail, VIPER_POST_CLONE_ON_ERROR decides: retry (default: a warning, tried again with the next
// viper up), ignore (a warning, never tried again) or abort (the start fails, server retained,
// tried again with the next viper up). The outcome goes to the log.
func (r *runner) postClone(ctx context.Context, repoURL, workdir string) (int, error) {
	c := r.c
	cmds := strings.TrimSpace(c.Get("VIPER_POST_CLONE"))
	if cmds == "" || repoURL == "" {
		return 0, nil
	}
	onError := c.Setting("VIPER_POST_CLONE_ON_ERROR", "retry")
	// only right after the clone: the marker that clone.sh set
	marker := workdir + "/.git/viper-post-clone-pending"
	out, _, err := r.d.Run(ctx, "", "ssh", "-n", "-F", c.SSHConfig, c.SSHAlias, "test -e "+model.QuoteAll(marker)+" && echo pending; true")
	if err == nil && !strings.Contains(out, "pending") {
		r.lg.Event("post-clone commands not run (not a fresh clone, or they already ran)")
		return 0, nil
	}
	if err == nil {
		// through stdin into a 600 file: the commands (they may hold tokens) stay out of argv
		err = r.ssh(ctx, cmds+"\n", "umask 077 && mkdir -p ~/.config/viper && cat > ~/.config/viper/post-clone.sh")
	}
	if err == nil {
		err = r.script(ctx, postCloneScript, workdir, onError)
	}
	if err == nil {
		r.lg.Event("post-clone commands ran")
		return 0, nil
	}
	if interrupted(ctx) {
		return 1, driving.ErrShown
	}
	r.lg.Event("post-clone commands FAILED (on error: %s): %v", onError, err)
	if onError == "abort" {
		return r.fail("the VIPER_POST_CLONE commands failed (log on the server: ~/.config/viper/post-clone.log). Server retained; rerun viper up %s", c.Name)
	}
	again := "rerun viper up " + c.Name + " to try again"
	if onError == "ignore" {
		again = "not tried again (VIPER_POST_CLONE_ON_ERROR=ignore)"
	}
	r.warnf(ctx, "WARNING: the VIPER_POST_CLONE commands failed (log on the server: ~/.config/viper/post-clone.log); %s\n", again)
	return 0, nil
}

// codeServer installs code-server (VS Code in the browser), opt-in with VIPER_CODE_SERVER=1 (not
// fatal): the official installer (a .deb, installed with sudo), listening on 127.0.0.1 only; reach
// it through an SSH tunnel. The password goes through stdin into a 600 file, not into argv or
// user-data.
func (r *runner) codeServer(ctx context.Context) {
	c := r.c
	if c.Setting("VIPER_CODE_SERVER", "0") != "1" {
		return
	}
	rerun := "viper up " + c.Name
	const port = "8080"
	r.printf("code-server: installing (skipped if present) ...\n")
	install := "command -v code-server >/dev/null || { mkdir -p ~/.config/viper && f=$(mktemp) &&\n" +
		"      curl -fsSL -o \"$f\" https://code-server.dev/install.sh &&\n" +
		"      sh \"$f\" </dev/null >~/.config/viper/code-server-install.log 2>&1; rc=$?; rm -f \"$f\";\n" +
		"      [ $rc = 0 ] || tail -n 15 ~/.config/viper/code-server-install.log; exit $rc; }"
	if err := r.ssh(ctx, "", install); err != nil {
		r.warnf(ctx, "WARNING: code-server installation failed (log on the server: ~/.config/viper/code-server-install.log); rerun %s\n", rerun)
		return
	}
	// JSON strings are valid YAML: any character in the password is quoted correctly
	pw, _ := json.Marshal(c.Get("VIPER_CODE_SERVER_PASSWORD"))
	config := "bind-addr: 127.0.0.1:" + port + "\nauth: password\ncert: false\npassword: " + string(pw) + "\n"
	cmd := "umask 077 && mkdir -p ~/.config/code-server && cat > ~/.config/code-server/config.yaml &&\n" +
		"        sudo systemctl enable code-server@builder >/dev/null 2>&1 && sudo systemctl restart code-server@builder"
	if err := r.ssh(ctx, config, cmd); err != nil {
		r.warnf(ctx, "WARNING: code-server could not be configured or started (journalctl -u code-server@builder on the server); rerun %s\n", rerun)
		return
	}
	r.printf("code-server: running on the server at 127.0.0.1:%s; tunnel: ssh -L %s:127.0.0.1:%s %s, then http://127.0.0.1:%s\n", port, port, port, c.SSHAlias, port)
}

// aptPackages are the packages of cloud-init.yaml (the list under packages:) and Docker's own,
// which runcmd installs: what the log shows as installed.
func aptPackages() []string {
	var pkgs []string
	in := false
	for _, l := range strings.Split(string(hetznerviper.CloudInit), "\n") {
		switch {
		case strings.HasPrefix(l, "packages:"):
			in = true
		case len(l) > 0 && l[0] >= 'a' && l[0] <= 'z' && strings.Contains(l, ":"):
			in = false
		case in && strings.HasPrefix(l, "  - "):
			if f := strings.Fields(l); len(f) >= 2 {
				pkgs = append(pkgs, f[1])
			}
		}
	}
	return append(pkgs, "docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin")
}

// inventory writes what is installed into the log (logs/<server>.log): the server and its own
// logs are gone once it is deleted. Informative only.
func (r *runner) inventory(ctx context.Context, brew string) {
	r.printf("== installed on the server ==\n")
	if err := r.script(ctx, inventoryScript, append([]string{brew}, aptPackages()...)...); err != nil {
		r.printf("(inventory not available)\n")
	}
}
