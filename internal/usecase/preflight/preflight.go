// Package preflight does the read-only checks before a machine is created. It creates and
// changes nothing. The wording of the lines is that of the script.
package preflight

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"unicode"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/dns"
)

// Cloud is the provider as far as the checks read it.
type Cloud interface {
	Title() string
	Validate(ctx context.Context, c model.Check) []model.Finding
}

// Deps are the outside world of the checks.
type Deps struct {
	Cloud Cloud
	DNS   dns.Checker
	HTTP  *http.Client
	// GitLsRemote reports whether a repository can be read without credentials; nil if git is
	// not installed.
	GitLsRemote func(ctx context.Context, url string) error
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

var redactRe = regexp.MustCompile(`^([a-z+]+://)[^/@]+@`)

// RedactURL hides credentials in a URL (https://user:token@host -> https://***@host).
func RedactURL(u string) string { return redactRe.ReplaceAllString(u, "${1}***@") }

// RedactTaps does the same for "owner/repo=URL" tap entries, as one line.
func RedactTaps(taps []string) string {
	out := make([]string, len(taps))
	for i, t := range taps {
		if name, u, ok := strings.Cut(t, "="); ok {
			out[i] = name + "=" + RedactURL(u)
		} else {
			out[i] = t
		}
	}
	return strings.Join(out, " ")
}

// ResolveRepoURL is the repository of the machine: the argument, else REPO_URL from the settings,
// else the one this server was started with.
func ResolveRepoURL(c *model.Machine, arg string) (url string, remembered bool) {
	if arg != "" {
		return arg, false
	}
	if u := c.Get("REPO_URL"); u != "" {
		return u, false
	}
	if b, err := os.ReadFile(c.RepoState); err == nil && len(b) > 0 {
		return strings.TrimRight(string(b), "\n"), true
	}
	return "", false
}

// Run runs all checks, printing to out, and returns the number of problems found.
func Run(ctx context.Context, c *model.Machine, repoArg string, d Deps, out, errOut io.Writer) int {
	r := &report{out: out}
	repoURL, _ := ResolveRepoURL(c, repoArg)

	localSetup(r, c)
	cloudChecks(ctx, r, c, d)

	if c.HasDNS() {
		fmt.Fprintln(out, "== Cloudflare DNS")
	} else {
		fmt.Fprintln(out, "== DNS (none)")
	}
	lines, ok, err := d.DNS.Check(ctx, c.DNSDomain, c.DNSHost)
	for _, l := range lines {
		fmt.Fprintln(out, l)
	}
	if err != nil {
		fmt.Fprintln(errOut, err)
	}
	if err != nil || !ok {
		r.failures++
	}

	githubChecks(ctx, r, c, d, repoURL)
	gitIdentity(r, c)
	claudeChecks(ctx, r, c, d)
	homebrewChecks(r, c, repoURL)
	swapCheck(r, c)
	codeServerCheck(r, c)
	repositoryChecks(ctx, r, c, d, repoURL)

	fmt.Fprintln(out)
	if r.failures > 0 {
		fmt.Fprintf(out, "Preflight: %d problem(s); nothing was created.\n", r.failures)
	} else {
		fmt.Fprintln(out, "Preflight OK.")
	}
	return r.failures
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func localSetup(r *report, c *model.Machine) {
	fmt.Fprintf(r.out, "== settings and local setup (%s)\n", c.ServerName)
	dnsName := c.DNSHost
	if !c.HasDNS() {
		dnsName = "none"
	}
	r.ok("settings complete: %s in %s, %s, DNS %s", c.ServerType, c.ServerLocation, c.ServerImage, dnsName)
	ca := filepath.Join(c.SSHDir, "hetzner_claude_host_ca")
	if exists(c.SSHKey) && exists(c.SSHKey+".pub") && exists(ca) {
		r.ok("login key %s and host CA present", c.SSHKey)
	} else {
		r.bad("login key or host CA missing: Run viper setup first")
	}
	known, _ := os.ReadFile(filepath.Join(c.SSHDir, "known_hosts_hetzner_claude_ca"))
	switch {
	case !c.HasDNS():
		// other devices verify the server by its DNS name; without one there is nothing to trust
	case strings.Contains(string(known), "@cert-authority *."+c.DNSDomain+" "):
		r.ok("host CA trusted for *.%s", c.DNSDomain)
	default:
		r.bad("host CA trust for *.%s missing (new or changed DNS_DOMAIN?): run viper setup", c.DNSDomain)
	}
	keyFileChecks(r, c)
	permissionChecks(r, c)
	cidrChecks(r, c)
}

// keyFileChecks checks that the deploy key and iPad public key files can be read.
func keyFileChecks(r *report, c *model.Machine) {
	for _, name := range []string{"GIT_DEPLOY_KEY_FILE", "IPAD_SSH_PUBLIC_KEY_FILE"} {
		f := model.ExpandHome(c.Get(name), userHome(c))
		if f == "" {
			continue
		}
		b, err := os.ReadFile(f)
		switch {
		case err != nil:
			r.bad("%s: File not readable: %s", name, f)
		case name == "IPAD_SSH_PUBLIC_KEY_FILE" && !isPublicKey(string(b)):
			// it goes into the user-data, which the server and the Hetzner project can read
			r.bad("IPAD_SSH_PUBLIC_KEY_FILE must be a public key (.pub), not a private key: %s", f)
		default:
			r.ok("%s readable", name)
		}
	}
}

// permissionChecks warns about settings files readable by others and an unwritable ~/.ssh/config.
func permissionChecks(r *report, c *model.Machine) {
	// config files hold API tokens: nobody but the owner should read them
	for _, f := range []string{".env.local", filepath.Join("machines", c.Name+".env.local")} {
		if runtime.GOOS == "windows" {
			break // no mode bits there
		}
		if st, err := os.Stat(filepath.Join(c.Dir, f)); err == nil && st.Mode().Perm()&0o077 != 0 {
			r.warn("%s is readable by other users; it may hold tokens: chmod 600 %s", f, f)
		}
	}
	sshConfig := filepath.Join(c.SSHDir, "config")
	if exists(sshConfig) {
		target, err := filepath.EvalSymlinks(sshConfig)
		if err != nil {
			target = sshConfig
		}
		if f, err := os.OpenFile(target, os.O_WRONLY, 0); err != nil {
			r.warn("~/.ssh/config (%s) is not writable: viper up cannot add 'Include %s'; ssh <alias> and VS Code need it", target, c.SSHConfig)
		} else {
			_ = f.Close() // only the open mattered
		}
	}
}

// cidrChecks validates SSH_ALLOWED_CIDRS.
func cidrChecks(r *report, c *model.Machine) {
	v4 := regexp.MustCompile(`^([0-9]{1,3}\.){3}[0-9]{1,3}/[0-9]{1,2}$`)
	v6 := regexp.MustCompile(`^[0-9a-fA-F:]+/[0-9]{1,3}$`)
	cidrs := c.Setting("SSH_ALLOWED_CIDRS", "0.0.0.0/0,::/0")
	cidrOK := true
	for _, n := range strings.Split(strings.TrimSuffix(cidrs, ","), ",") {
		if !v4.MatchString(n) && !v6.MatchString(n) {
			r.bad("SSH_ALLOWED_CIDRS: not a network: %s", n)
			cidrOK = false
		}
	}
	if cidrOK {
		r.ok("SSH_ALLOWED_CIDRS: %s", cidrs)
	}
}

func userHome(c *model.Machine) string { return filepath.Dir(c.SSHDir) }

// isPublicKey: one OpenSSH public key line (type, base64 blob), as ssh-keygen -l would accept.
func isPublicKey(s string) bool {
	if strings.Contains(s, "PRIVATE KEY") {
		return false
	}
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		if len(f) < 2 || (!strings.HasPrefix(f[0], "ssh-") && !strings.HasPrefix(f[0], "ecdsa-") && !strings.HasPrefix(f[0], "sk-")) {
			return false
		}
		_, err := base64.StdEncoding.DecodeString(f[1])
		return err == nil
	}
	return false
}

func cloudChecks(ctx context.Context, r *report, c *model.Machine, d Deps) {
	fmt.Fprintln(r.out, "== "+d.Cloud.Title())
	pub, _ := os.ReadFile(c.SSHKey + ".pub")
	for _, f := range d.Cloud.Validate(ctx, model.Check{
		Machine: c.Name, ServerName: c.ServerName, KeyName: c.KeyName, KeyFile: c.SSHKey, PublicKey: string(pub),
		Spec: model.ServerSpec{Name: c.ServerName, Size: c.ServerType, Region: c.ServerLocation, Image: c.ServerImage},
	}) {
		switch f.Level {
		case model.OK:
			r.ok("%s", f.Text)
		case model.Info:
			r.info("%s", f.Text)
		case model.Warn:
			r.warn("%s", f.Text)
		default:
			r.bad("%s", f.Text)
		}
	}
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 32 || r == 127 {
			return true
		}
	}
	return false
}

func isGraphicASCIIOrVisible(s string) bool {
	for _, r := range s {
		if r <= 32 || r == 127 || unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// Service is the use case Preflight over its dependencies; it implements driving.Preflight.
type Service struct{ Deps Deps }

// Preflight runs the checks; see Run.
func (s Service) Preflight(ctx context.Context, c *model.Machine, repoArg string, out, errOut io.Writer) int {
	return Run(ctx, c, repoArg, s.Deps, out, errOut)
}
