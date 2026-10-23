// Package preflight is preflight.sh: read-only checks before a machine is created. It creates and
// changes nothing. The wording of the lines is that of the script.
package preflight

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/pt9912/hetzner-viper/internal/config"
	"github.com/pt9912/hetzner-viper/internal/hetzner"
)

// Hetzner is the part of the Hetzner API the checks read.
type Hetzner interface {
	ServerNames(ctx context.Context) ([]string, error)
	ServerType(ctx context.Context, name string) (*hetzner.ServerType, error)
	LocationExists(ctx context.Context, name string) (bool, error)
	ImageExists(ctx context.Context, name, architecture string) (bool, error)
	SSHKeys(ctx context.Context) ([]hetzner.SSHKey, error)
}

// DNS is the read-only check of the Cloudflare record.
type DNS interface {
	Check(ctx context.Context, domain, host string) (lines []string, ok bool, err error)
}

// Deps are the outside world of the checks.
type Deps struct {
	Hetzner Hetzner
	DNS     DNS
	HTTP    *http.Client
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
func ResolveRepoURL(c *config.Config, arg string) (url string, remembered bool) {
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
func Run(ctx context.Context, c *config.Config, repoArg string, d Deps, out, errOut io.Writer) int {
	r := &report{out: out}
	repoURL, _ := ResolveRepoURL(c, repoArg)

	localSetup(r, c)
	hetznerChecks(ctx, r, c, d)

	fmt.Fprintln(out, "== Cloudflare DNS")
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

func localSetup(r *report, c *config.Config) {
	fmt.Fprintf(r.out, "== settings and local setup (%s)\n", c.ServerName)
	r.ok("settings complete: %s in %s, %s, DNS %s", c.ServerType, c.ServerLocation, c.ServerImage, c.DNSHost)
	ca := filepath.Join(c.SSHDir, "hetzner_claude_host_ca")
	if exists(c.SSHKey) && exists(c.SSHKey+".pub") && exists(ca) {
		r.ok("login key %s and host CA present", c.SSHKey)
	} else {
		r.bad("login key or host CA missing: Run viper setup first")
	}
	known, _ := os.ReadFile(filepath.Join(c.SSHDir, "known_hosts_hetzner_claude_ca"))
	if strings.Contains(string(known), "@cert-authority *."+c.DNSDomain+" ") {
		r.ok("host CA trusted for *.%s", c.DNSDomain)
	} else {
		r.bad("host CA trust for *.%s missing (new or changed DNS_DOMAIN?): run viper setup", c.DNSDomain)
	}
	for _, name := range []string{"GIT_DEPLOY_KEY_FILE", "IPAD_SSH_PUBLIC_KEY_FILE"} {
		f := config.ExpandHome(c.Get(name), userHome(c))
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
	// config files hold API tokens: nobody but the owner should read them
	for _, f := range []string{".env.local", filepath.Join("machines", c.Machine+".env.local")} {
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
			f.Close()
		}
	}
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

func userHome(c *config.Config) string { return filepath.Dir(c.SSHDir) }

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
		if len(f) < 2 || !(strings.HasPrefix(f[0], "ssh-") || strings.HasPrefix(f[0], "ecdsa-") || strings.HasPrefix(f[0], "sk-")) {
			return false
		}
		_, err := base64.StdEncoding.DecodeString(f[1])
		return err == nil
	}
	return false
}

// FingerprintMD5 is the colon-separated MD5 fingerprint of an OpenSSH public key file, "" if
// it cannot be read.
func FingerprintMD5(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return ""
	}
	blob, err := base64.StdEncoding.DecodeString(f[1])
	if err != nil {
		return ""
	}
	sum := md5.Sum(blob)
	parts := make([]string, len(sum))
	for i, x := range sum {
		parts[i] = fmt.Sprintf("%02x", x)
	}
	return strings.Join(parts, ":")
}

func formatGB(m float32) string { return fmt.Sprintf("%g", m) }

func hetznerChecks(ctx context.Context, r *report, c *config.Config, d Deps) {
	fmt.Fprintln(r.out, "== Hetzner Cloud")
	names, err := d.Hetzner.ServerNames(ctx)
	if err != nil {
		r.bad("Hetzner API not usable (token, project, network): %s", hetzner.Error(err))
		return
	}
	r.ok("Hetzner token can read the project (write access is only seen when viper up creates something)")
	st, err := d.Hetzner.ServerType(ctx, c.ServerType)
	switch {
	case err != nil:
		r.bad("server type %s could not be read: %s", c.ServerType, hetzner.Error(err))
	case st == nil:
		r.bad("server type %s does not exist (see the server types in the Hetzner Console)", c.ServerType)
	default:
		r.ok("server type %s exists (%d vCPU, %s GB RAM, %d GB disk), architecture %s", c.ServerType, st.Cores, formatGB(st.MemoryGB), st.DiskGB, st.Architecture)
		locationChecks(ctx, r, c, d, st)
		if ok, err := d.Hetzner.ImageExists(ctx, c.ServerImage, st.Architecture); err != nil {
			r.bad("image %s could not be read: %s", c.ServerImage, hetzner.Error(err))
		} else if ok {
			r.ok("image %s exists for %s", c.ServerImage, st.Architecture)
		} else {
			r.bad("image %s does not exist for %s (see the system images in the Hetzner Console)", c.ServerImage, st.Architecture)
		}
	}
	keys, err := d.Hetzner.SSHKeys(ctx)
	if err != nil {
		r.bad("SSH keys could not be listed: %s", hetzner.Error(err))
		return
	}
	fp := FingerprintMD5(c.SSHKey + ".pub")
	existing, named := "", false
	for _, k := range keys {
		if fp != "" && strings.EqualFold(k.Fingerprint, fp) && existing == "" {
			existing = k.Name
		}
		if k.Name == c.KeyName {
			named = true
		}
	}
	switch {
	case existing != "":
		r.ok("login key already in the project as \"%s\" (reused)", existing)
	case named:
		r.bad("SSH key %s in Hetzner belongs to a different public key than %s.pub", c.KeyName, c.SSHKey)
	default:
		r.info("login key will be uploaded as %s", c.KeyName)
	}
	for _, n := range names {
		if n == c.ServerName {
			r.info("server %s exists; viper up reuses it", c.ServerName)
			return
		}
	}
	r.info("server %s will be created", c.ServerName)
}

func locationChecks(ctx context.Context, r *report, c *config.Config, d Deps, st *hetzner.ServerType) {
	exists, err := d.Hetzner.LocationExists(ctx, c.ServerLocation)
	if err != nil {
		r.bad("location %s could not be read: %s", c.ServerLocation, hetzner.Error(err))
		return
	}
	if !exists {
		r.bad("location %s does not exist (see the locations in the Hetzner Console)", c.ServerLocation)
		return
	}
	r.ok("location %s exists", c.ServerLocation)
	// availability per location comes with the server type (the datacenter API was removed)
	var offered []string
	var here *hetzner.ServerTypeLocation
	for i, l := range st.Locations {
		if l.Available {
			offered = append(offered, l.Name)
		}
		if l.Name == c.ServerLocation {
			here = &st.Locations[i]
		}
	}
	list := strings.Join(offered, ", ")
	if list == "" {
		list = "none"
	}
	hint := fmt.Sprintf("set SERVER_LOCATION or SERVER_TYPE, e.g. in machines/%s.env.local", c.Machine)
	switch {
	case here == nil:
		r.bad("%s is not offered in %s (available in: %s); %s", c.ServerType, c.ServerLocation, list, hint)
	case !here.Available:
		r.bad("%s cannot currently be ordered in %s (available in: %s); %s", c.ServerType, c.ServerLocation, list, hint)
	default:
		r.ok("%s can currently be ordered in %s", c.ServerType, c.ServerLocation)
		if !here.UnavailableAfter.IsZero() {
			r.warn("%s is deprecated in %s and unavailable after %s", c.ServerType, c.ServerLocation, here.UnavailableAfter.UTC().Format("2006-01-02T15:04:05Z"))
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
