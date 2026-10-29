// Package check is check.sh: read-only checks of a running machine. The wording of the lines is
// that of the script.
package check

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
	"github.com/pt9912/hetzner-viper/internal/port/driven/sshconfig"
	"github.com/pt9912/hetzner-viper/internal/port/driven/system"
	"github.com/pt9912/hetzner-viper/internal/usecase/preflight"
)

// probe runs on the server and prints key=value lines; it changes nothing there.
//
//go:embed probe.sh
var probe string

// Deps are the outside world of the checks.
type Deps struct {
	Cloud interface {
		cloud.Describer
		ServerByName(ctx context.Context, name string) (*model.Server, error)
	}
	HTTP *http.Client // DNS over HTTPS
	// LookupIPv4 asks this computer's resolver.
	LookupIPv4 func(ctx context.Context, host string) ([]string, error)
	Nameserver string // the resolver's address, "" if unknown
	// Run runs a program; stdin may be empty.
	Run system.Runner
	// SSH writes the machine's ssh config if there is none yet.
	SSH sshconfig.Store
}

type report struct {
	out      io.Writer
	failures int
}

func (r *report) ok(format string, a ...any)   { fmt.Fprintf(r.out, "ok    "+format+"\n", a...) }
func (r *report) info(format string, a ...any) { fmt.Fprintf(r.out, "info  "+format+"\n", a...) }
func (r *report) warn(format string, a ...any) { fmt.Fprintf(r.out, "WARN  "+format+"\n", a...) }
func (r *report) bad(format string, a ...any) {
	fmt.Fprintf(r.out, "FAIL  "+format+"\n", a...)
	r.failures++
}

func (d Deps) client() *http.Client {
	if d.HTTP != nil {
		return d.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// dohQuery asks a DNS-over-HTTPS resolver (no dig needed); the records of the answer and authority sections.
type dohRecord struct {
	Type int    `json:"type"`
	Data string `json:"data"`
}

func (d Deps) doh(ctx context.Context, endpoint, name, typ string) (answer, authority []dohRecord, err error) {
	q := url.Values{"name": {name}, "type": {typ}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("accept", "application/dns-json")
	resp, err := d.client().Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }() // read-only body
	if resp.StatusCode >= 400 {
		return nil, nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var v struct {
		Answer    []dohRecord `json:"Answer"`
		Authority []dohRecord `json:"Authority"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&v); err != nil {
		return nil, nil, err
	}
	return v.Answer, v.Authority, nil
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// Run runs the checks and returns the number of problems. A non-nil error means the checks could
// not run (no server, no ssh connection): the reason was printed.
func Run(ctx context.Context, c *model.Machine, d Deps, out, errOut io.Writer) (int, error) {
	r := &report{out: out}
	repoURL, _ := preflight.ResolveRepoURL(c, "")

	srv, err := d.Cloud.ServerByName(ctx, c.ServerName)
	if err != nil {
		return 0, fmt.Errorf("%s: listing the servers failed (check %s): %s", d.Cloud.Name(), d.Cloud.Hint(), d.Cloud.Describe(err))
	}
	if srv == nil {
		fmt.Fprintf(errOut, "Server %s does not exist (start it with viper up %s)\n", c.ServerName, c.Name)
		return 0, fmt.Errorf("server %s does not exist", c.ServerName)
	}
	ip := srv.IPv4
	if _, err := os.Stat(c.SSHConfig); err != nil {
		if err := d.SSH.WriteConfig(c, ip); err != nil {
			return 0, err
		}
	}

	if c.HasDNS() {
		dnsChecks(ctx, r, c, d, ip)
		sshAsAnotherDevice(ctx, r, c, d)
	} else {
		fmt.Fprintln(out, "== DNS (none)")
		r.info("DNS_PROVIDER=none: no DNS name to check; connect with viper ssh %s", c.Name)
	}

	fmt.Fprintf(out, "== server (%s, %s)\n", c.ServerName, ip)
	workdir := c.WorkDir(repoURL)
	brewfile := ""
	if c.Setting("VIPER_BREWFILE", "0") == "1" && repoURL != "" {
		brewfile = workdir + "/Brewfile"
	}
	askClaude := ""
	if c.Get("VIPER_CLAUDE_TOKEN") != "" {
		askClaude += "1"
	}
	if c.Get("VIPER_ANTHROPIC_BASE_URL") != "" {
		askClaude += "1"
	}
	brew := c.Setting("VIPER_BREW", "/home/linuxbrew/.linuxbrew/bin/brew")
	remote := "bash -s -- " + model.QuoteAll(workdir, askClaude, brew, c.Get("VIPER_BREW_TAPS"), c.Get("VIPER_BREW_PACKAGES"),
		brewfile, c.Get("VIPER_BREW_TRUST"), c.Setting("VIPER_CODE_SERVER", "0"), "8080")
	stdout, _, err := d.Run(ctx, probe, "ssh", "-F", c.SSHConfig, c.SSHAlias, remote)
	if err != nil {
		r.bad("SSH to %s failed", c.SSHAlias)
		fmt.Fprintf(out, "\nCheck: %d problem(s).\n", r.failures)
		return r.failures, fmt.Errorf("SSH to %s failed", c.SSHAlias)
	}
	serverChecks(r, c, parseProbe(stdout), workdir, repoURL)

	fmt.Fprintln(out)
	if r.failures > 0 {
		fmt.Fprintf(out, "Check: %d problem(s).\n", r.failures)
	} else {
		fmt.Fprintln(out, "Check OK.")
	}
	return r.failures, nil
}

func dnsChecks(ctx context.Context, r *report, c *model.Machine, d Deps, ip string) {
	fmt.Fprintf(r.out, "== DNS (%s)\n", c.DNSHost)
	for _, resolver := range []struct{ name, url string }{
		{"Cloudflare 1.1.1.1", "https://cloudflare-dns.com/dns-query"}, {"Google 8.8.8.8", "https://dns.google/resolve"},
	} {
		publicResolverCheck(ctx, r, c, d, resolver.name, resolver.url, ip)
	}
	// the resolver this process uses
	where := "this computer's resolver"
	if d.Nameserver != "" {
		where += " (" + d.Nameserver + ")"
	}
	local, _ := d.LookupIPv4(ctx, c.DNSHost)
	first := ""
	if len(local) > 0 {
		first = local[0]
	}
	if first == ip {
		r.ok("%s resolves it to %s", where, ip)
		return
	}
	// A resolver that looked the name up while it did not exist (e.g. between down and up)
	// may keep answering "no such name" for the zone's negative-caching time (last SOA field).
	negTTL := negativeCacheTTL(ctx, c, d)
	shown := first
	if shown == "" {
		shown = "nothing"
	}
	r.info("%s resolves it to '%s'. If the public resolvers above are right, it still", where, shown)
	r.info("  remembers 'no such name' from a lookup while the record did not exist, for up to %s s (often a VPN's DNS server)", negTTL)
}

// publicResolverCheck compares the A records one public resolver returns with the server's IP.
func publicResolverCheck(ctx context.Context, r *report, c *model.Machine, d Deps, name, url, ip string) {
	answer, _, err := d.doh(ctx, url, c.DNSHost, "A")
	if err != nil {
		r.warn("%s could not be asked", name)
		return
	}
	var addrs []string
	for _, a := range answer {
		if a.Type == 1 {
			addrs = append(addrs, a.Data)
		}
	}
	joined := strings.Join(addrs, ",")
	if strings.Contains(","+joined+",", ","+ip+",") {
		r.ok("%s resolves to %s", name, ip)
	} else {
		if joined == "" {
			joined = "nothing"
		}
		r.bad("%s resolves to '%s', expected %s (new record? wait a few minutes)", name, joined, ip)
	}
}

// negativeCacheTTL is the zone's negative-caching time (last SOA field), or "?".
func negativeCacheTTL(ctx context.Context, c *model.Machine, d Deps) string {
	negTTL := "?"
	if answer, authority, err := d.doh(ctx, "https://cloudflare-dns.com/dns-query", c.DNSDomain, "SOA"); err == nil {
		for _, rec := range append(answer, authority...) {
			if rec.Type == 6 {
				if f := strings.Fields(rec.Data); len(f) > 0 {
					negTTL = f[len(f)-1]
				}
				break
			}
		}
	}
	return negTTL
}

var certRe = regexp.MustCompile(`ssh-ed25519-cert-v01@openssh\.com [A-Za-z0-9+/=]+`)

func sshAsAnotherDevice(ctx context.Context, r *report, c *model.Machine, d Deps) {
	fmt.Fprintln(r.out, "== SSH like another device (host CA, DNS name)")
	caKnown := filepath.Join(c.SSHDir, "known_hosts_hetzner_claude_ca")
	_, stderr, err := d.Run(ctx, "", "ssh", "-n", "-F", os.DevNull, "-i", c.SSHKey, "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile="+caKnown,
		"-o", "HostKeyAlgorithms=ssh-ed25519-cert-v01@openssh.com", "builder@"+c.DNSHost, "true")
	if err == nil {
		r.ok("connection to %s verified by the host CA (certificate valid for this name)", c.DNSHost)
	} else {
		r.bad("connection to %s with the host CA failed: %s", c.DNSHost, lastLine(stderr))
	}
	// expiry of the host certificate (only informative; ssh-keyscan -c prints "<type> <base64>")
	valid := ""
	if scan, _, err := d.Run(ctx, "", "ssh-keyscan", "-c", "-t", "ed25519", c.DNSHost); err == nil {
		if cert := certRe.FindString(scan); cert != "" {
			valid = certValidity(ctx, d, cert)
		}
	}
	if valid != "" {
		r.info("host certificate %s; afterwards only computers with a pinned host key can verify the server", valid)
	} else {
		r.info("host certificate validity could not be read")
	}
}

func parseProbe(out string) map[string]string {
	vals := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			if _, seen := vals[k]; !seen {
				vals[k] = v
			}
		}
	}
	return vals
}

var (
	answerOKRe = regexp.MustCompile(`^[Oo][Kk][.!]?$`)
	urlAuthRe  = regexp.MustCompile(`([a-z+]+://)[^/@ ]+@`)
	startsNZ   = regexp.MustCompile(`^[1-9]`)
)

func serverChecks(r *report, c *model.Machine, val map[string]string, workdir, repoURL string) {
	v := func(k string) string { return val[k] }
	rerun := "viper up " + c.Name

	cloudInitChecks(r, v)
	claudeChecks(r, c, v, rerun)
	ghChecks(r, c, v, rerun)
	if v("git_name") != "" && v("git_email") != "" {
		r.ok("git commits as %s <%s>", v("git_name"), v("git_email"))
	} else {
		r.warn("git user.name/user.email are not set on the server: commits fail (VIPER_GIT_USER_NAME, VIPER_GIT_USER_EMAIL, then rerun %s)", rerun)
	}
	if v("docker") == "ok" {
		r.ok("docker running")
	} else {
		r.bad("docker not running")
	}
	swapAndCodeServerChecks(r, c, v, rerun)
	if c.Setting("VIPER_HOMEBREW", "1") == "1" {
		brewChecks(r, c, v, rerun)
	}
	if repoURL != "" {
		if v("repo") != "" {
			r.ok("repository %s at %s (%s uncommitted changes)", workdir, v("repo"), strings.TrimSpace(v("repo_changes")))
		} else {
			r.bad("repository missing: %s", workdir)
		}
	}
	if v("sshd") == "authenticationmethods publickey;passwordauthentication no;permitrootlogin no;" {
		r.ok("sshd: public key only, no password, no root login")
	} else {
		r.bad("sshd hardening not in effect: %s", v("sshd"))
	}
	if v("reboot") == "required" {
		r.warn("updates need a reboot: viper ssh %s --cmd 'sudo reboot' (tmux sessions end)", c.Name)
	}
	r.info("memory: %s; disk: %s", v("mem"), v("disk"))
}

func cloudInitChecks(r *report, v func(string) string) {
	switch v("cloudinit") {
	case "done":
		r.ok("cloud-init: done")
	case "disabled":
		r.ok("cloud-init: done (disabled after the first boot)")
	case "":
		r.bad("cloud-init: no status")
	default:
		r.warn("cloud-init: %s", v("cloudinit"))
	}
	if startsNZ.MatchString(v("cloudinit_errors")) {
		r.warn("cloud-init reported %s error(s) at the first boot, e.g.: %s (see /var/log/cloud-init.log)", v("cloudinit_errors"), v("cloudinit_error"))
	}
}

func claudeChecks(r *report, c *model.Machine, v func(string) string, rerun string) {
	// the test request must really answer OK, not merely mention it in an error
	answeredOK := answerOKRe.MatchString(strings.Join(strings.Fields(v("claude_answer")), ""))
	baseURL, claudeModel, token := c.Get("VIPER_ANTHROPIC_BASE_URL"), c.Get("VIPER_CLAUDE_MODEL"), c.Get("VIPER_CLAUDE_TOKEN")
	switch {
	case v("claude_version") == "":
		r.bad("Claude Code not installed")
	case baseURL != "":
		if v("claude_auth") != "gateway "+claudeModel+" "+baseURL {
			r.bad("Claude Code %s: not configured for %s at %s (has: %s; rerun %s)", v("claude_version"), claudeModel, preflight.RedactURL(baseURL),
				urlAuthRe.ReplaceAllString(v("claude_auth"), "${1}***@"), rerun)
		} else if answeredOK {
			r.ok("Claude Code %s: model %s at %s answers", v("claude_version"), claudeModel, preflight.RedactURL(baseURL))
		} else {
			r.bad("Claude Code %s: no answer from %s: %s", v("claude_version"), claudeModel, v("claude_answer"))
		}
	case token != "":
		if v("claude_auth") != "token" {
			r.bad("Claude Code %s: token not loaded in shells (rerun %s)", v("claude_version"), rerun)
		} else if answeredOK {
			r.ok("Claude Code %s: logged in via VIPER_CLAUDE_TOKEN and answers", v("claude_version"))
		} else {
			r.bad("Claude Code %s: no answer with the token: %s", v("claude_version"), v("claude_answer"))
		}
	default:
		r.info("Claude Code %s: no VIPER_CLAUDE_TOKEN or VIPER_ANTHROPIC_BASE_URL, log in with claude (/login)", v("claude_version"))
	}
}

func ghChecks(r *report, c *model.Machine, v func(string) string, rerun string) {
	switch {
	case v("gh_version") == "":
		r.bad("gh not installed")
	case v("gh_auth") == "yes":
		r.ok("%s: logged in", v("gh_version"))
	case c.Get("VIPER_GH_TOKEN") != "":
		r.bad("%s: not logged in although VIPER_GH_TOKEN is set (rerun %s)", v("gh_version"), rerun)
	default:
		r.info("%s: not logged in (no VIPER_GH_TOKEN)", v("gh_version"))
	}
}

func swapAndCodeServerChecks(r *report, c *model.Machine, v func(string) string, rerun string) {
	if swap := c.Setting("VIPER_SWAP", "0"); swap != "0" {
		if mb, _ := strconv.Atoi(v("swap_mb")); mb > 0 {
			r.ok("swap active (%d MB)", mb)
		} else {
			r.bad("swap not active (VIPER_SWAP=%s; rerun %s)", swap, rerun)
		}
	}
	if c.Setting("VIPER_CODE_SERVER", "0") == "1" {
		switch {
		case v("code_server_version") == "":
			r.bad("code-server not installed (rerun %s; log: ~/.config/viper/code-server-install.log)", rerun)
		case v("code_server_active") != "active" || v("code_server_http") != "yes":
			r.bad("code-server %s is not running (rerun %s; journalctl -u code-server@builder)", v("code_server_version"), rerun)
		default:
			r.ok("code-server %s on 127.0.0.1:8080 (tunnel: ssh -L 8080:127.0.0.1:8080 %s)", v("code_server_version"), c.SSHAlias)
		}
	}
}

func brewChecks(r *report, c *model.Machine, v func(string) string, rerun string) {
	switch {
	case v("brew_version") == "":
		r.bad("Homebrew not installed (rerun %s; log: ~/.config/viper/homebrew-install.log)", rerun)
		return
	case v("brew_path") != "yes":
		r.bad("%s installed but not in the shells' PATH (rerun %s)", v("brew_version"), rerun)
		return
	}
	r.ok("%s", v("brew_version"))
	taps, trust, pkgs := c.Get("VIPER_BREW_TAPS"), c.Get("VIPER_BREW_TRUST"), c.Get("VIPER_BREW_PACKAGES")
	switch {
	case v("brew_missing") != "":
		r.bad("Homebrew: missing %s (rerun %s; log: ~/.config/viper/brew-packages.log)", v("brew_missing"), rerun)
	case taps+trust+pkgs != "":
		shown := preflight.RedactTaps(strings.Fields(taps))
		if taps != "" {
			shown += "; "
		}
		if trust != "" {
			shown += "trust " + trust + "; "
		}
		shown += pkgs
		r.ok("Homebrew: taps, trust entries and formulae present (%s)", shown)
	}
	switch v("brewfile") {
	case "ok":
		r.ok("Homebrew: Brewfile of the repository satisfied")
	case "missing":
		r.warn("Homebrew: Brewfile of the repository not fully installed (rerun %s)", rerun)
	}
}

// certValidity is the "Valid: ..." line ssh-keygen -L shows for a host certificate, or "".
func certValidity(ctx context.Context, d Deps, cert string) string {
	f, err := os.CreateTemp("", "cert")
	if err != nil {
		return ""
	}
	defer func() { _ = os.Remove(f.Name()) }() // temporary file
	_, werr := f.WriteString(cert + "\n")
	if cerr := f.Close(); werr != nil || cerr != nil {
		return ""
	}
	desc, _, err := d.Run(ctx, "", "ssh-keygen", "-L", "-f", f.Name())
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(desc, "\n") {
		if t := strings.TrimLeft(line, " "); strings.HasPrefix(t, "Valid: ") {
			return strings.TrimPrefix(t, "Valid: ")
		}
	}
	return ""
}

// Service is the use case Check over its dependencies; it implements driving.Check.
type Service struct{ Deps Deps }

// Check runs the checks; see Run.
func (s Service) Check(ctx context.Context, c *model.Machine, out, errOut io.Writer) (int, error) {
	return Run(ctx, c, s.Deps, out, errOut)
}
