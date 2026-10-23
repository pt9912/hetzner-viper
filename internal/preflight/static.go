package preflight

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/pt9912/hetzner-viper/internal/config"
)

var emailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+$`)

func gitIdentity(r *report, c *config.Config) {
	name, email := c.Get("VIPER_GIT_USER_NAME"), c.Get("VIPER_GIT_USER_EMAIL")
	switch {
	case hasControl(name) || hasControl(email):
		r.bad("VIPER_GIT_USER_NAME / VIPER_GIT_USER_EMAIL: no line breaks or control characters")
	case email != "" && !emailRe.MatchString(email):
		r.bad("VIPER_GIT_USER_EMAIL is not an e-mail address: %s", email)
	case name != "" && email != "":
		r.ok("git commits on the server as %s <%s>", name, email)
	case c.Get("VIPER_GH_TOKEN") != "":
		have := ""
		if name != "" {
			have += "name " + name + ", "
		}
		if email != "" {
			have += "e-mail " + email + ", "
		}
		r.info("git identity: %sthe rest from the GitHub account (name, noreply address on github.com)", have)
	default:
		r.info("no VIPER_GIT_USER_NAME / VIPER_GIT_USER_EMAIL: git commits on the server fail until user.name and user.email are set")
	}
}

var (
	trustRe = regexp.MustCompile(`^((tap|formula|cask|command):)?[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(/[A-Za-z0-9][A-Za-z0-9@+_.-]*)?$`)
	tapRe   = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(=(https://|ssh://|git@)\S+)?$`)
	tapCred = regexp.MustCompile(`=https?://[^/@]+@`)
	pkgRe   = regexp.MustCompile(`^([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/)?[A-Za-z0-9][A-Za-z0-9@+_.-]*$`)
)

func homebrewChecks(r *report, c *config.Config, repoURL string) {
	brew := c.Setting("VIPER_HOMEBREW", "1")
	if brew == "1" {
		r.info("Homebrew will be installed on the server (VIPER_HOMEBREW=0 to skip; about 1-2 minutes)")
	}
	taps, trust, pkgs := strings.Fields(c.Get("VIPER_BREW_TAPS")), strings.Fields(c.Get("VIPER_BREW_TRUST")), strings.Fields(c.Get("VIPER_BREW_PACKAGES"))
	if len(taps)+len(trust)+len(pkgs) > 0 {
		if brew != "1" {
			r.bad("VIPER_BREW_TAPS / VIPER_BREW_TRUST / VIPER_BREW_PACKAGES need VIPER_HOMEBREW=1")
		}
		for _, t := range trust {
			if !trustRe.MatchString(t) {
				r.bad("VIPER_BREW_TRUST: not a tap (owner/repo) or formula (owner/repo/name), optionally with cask:/command:: %s", t)
			}
		}
		for _, t := range taps {
			if !tapRe.MatchString(t) {
				r.bad("VIPER_BREW_TAPS: not a tap (owner/repo or owner/repo=URL): %s", RedactTaps([]string{t}))
			} else if tapCred.MatchString(t) {
				// the token would be stored in the tap's .git/config on the server and shown in logs
				r.bad("VIPER_BREW_TAPS: credentials in the tap URL (%s): use a deploy key (ssh URL) or a public tap", RedactTaps([]string{t}))
			}
		}
		for _, p := range pkgs {
			if !pkgRe.MatchString(p) {
				r.bad("VIPER_BREW_PACKAGES: not a formula (name or owner/repo/name): %s", p)
			}
		}
		orNone := func(s string) string {
			if s == "" {
				return "none"
			}
			return s
		}
		r.info("Homebrew taps: %s; trust: %s; formulae: %s", orNone(RedactTaps(taps)), orNone(strings.Join(trust, " ")), orNone(strings.Join(pkgs, " ")))
	}
	if c.Setting("VIPER_BREWFILE", "0") == "1" {
		if brew != "1" {
			r.bad("VIPER_BREWFILE=1 needs VIPER_HOMEBREW=1")
		}
		if repoURL == "" {
			r.info("VIPER_BREWFILE=1 without a repository: nothing to apply")
		} else {
			r.warn("the repository's Brewfile will be applied: it is Ruby code run by brew bundle, and its trusted: entries change Homebrew's trust store")
		}
	}
}

var (
	swapSizeRe   = regexp.MustCompile(`^[1-9][0-9]*[MG]$`)
	swapFactorRe = regexp.MustCompile(`^[0-9]*\.?[0-9]+$`)
)

func swapCheck(r *report, c *config.Config) {
	v := c.Setting("VIPER_SWAP", "0")
	if v == "0" {
		return
	}
	if swapSizeRe.MatchString(v) {
		r.ok("swap file of %s on the server", v)
		return
	}
	if swapFactorRe.MatchString(v) {
		if x, err := strconv.ParseFloat(v, 64); err == nil && x >= 0.1 && x <= 4 {
			r.ok("swap file of %s x the server's RAM", v)
			return
		}
	}
	r.bad("VIPER_SWAP must be 0, a factor of the RAM between 0.1 and 4 (e.g. 1, 0.5) or a size (e.g. 4G, 512M): %s", v)
}

func codeServerCheck(r *report, c *config.Config) {
	pw := c.Get("VIPER_CODE_SERVER_PASSWORD")
	switch v := c.Setting("VIPER_CODE_SERVER", "0"); v {
	case "1":
		switch {
		case pw == "":
			r.bad("VIPER_CODE_SERVER=1 needs VIPER_CODE_SERVER_PASSWORD (the login of code-server in the browser)")
		case !isGraphicASCIIOrVisible(pw) || utf8.RuneCountInString(pw) < 12:
			r.bad("VIPER_CODE_SERVER_PASSWORD: at least 12 characters, no spaces or line breaks")
		default:
			r.ok("code-server will be installed (VS Code in the browser, reachable only through an SSH tunnel to 127.0.0.1:%d)", 8080)
		}
	case "0":
		if pw != "" {
			r.info("VIPER_CODE_SERVER_PASSWORD is set but VIPER_CODE_SERVER is not 1: ignored")
		}
	default:
		r.bad("VIPER_CODE_SERVER must be 0 or 1: %s", v)
	}
}

var (
	credURLRe = regexp.MustCompile(`^[a-z+]+://[^/@]+@`)
	sshUserRe = regexp.MustCompile(`^ssh://[^/@:]+@`)
	sshRepoRe = regexp.MustCompile(`^(ssh://|[^/]+@[^/]+:)`)
)

func repositoryChecks(ctx context.Context, r *report, c *config.Config, d Deps, repoURL string) {
	fmt.Fprintln(r.out, "== repository")
	shown := RedactURL(repoURL)
	host := c.Setting("VIPER_GH_HOST", "github.com")
	token, deployKey := c.Get("VIPER_GH_TOKEN"), c.Get("GIT_DEPLOY_KEY_FILE")
	switch {
	case repoURL == "":
		r.info("no repository to clone")
	case strings.HasPrefix(repoURL, "-"):
		r.bad("REPO_URL must not start with '-': %s", shown)
	case credURLRe.MatchString(repoURL) && !sshUserRe.MatchString(repoURL):
		// a token in the URL would end up in .git/config on the server and in logs
		r.bad("REPO_URL contains credentials (%s): use VIPER_GH_TOKEN or GIT_DEPLOY_KEY_FILE instead", shown)
	case sshRepoRe.MatchString(repoURL):
		switch {
		case deployKey != "":
			r.info("SSH repository %s with deploy key: access is only checked by the clone on the server", shown)
		case token != "" && (strings.HasPrefix(repoURL, "git@"+host+":") || strings.HasPrefix(repoURL, "ssh://git@"+host+"/")):
			r.ok("SSH repository %s will be cloned over HTTPS with VIPER_GH_TOKEN", shown)
		default:
			r.bad("SSH repository %s needs GIT_DEPLOY_KEY_FILE (the server has no SSH key for the git host); or use an HTTPS URL, or VIPER_GH_TOKEN for %s", shown, host)
		}
	case token != "" && strings.HasPrefix(repoURL, "https://"+host+"/"):
		// checked through the GitHub API above
	case d.GitLsRemote != nil:
		if err := d.GitLsRemote(ctx, repoURL); err == nil {
			r.ok("repository %s reachable", shown)
		} else {
			r.bad("repository %s not reachable without credentials (private? set VIPER_GH_TOKEN or GIT_DEPLOY_KEY_FILE)", shown)
		}
	default:
		r.info("git not installed locally: repository not checked")
	}
}
