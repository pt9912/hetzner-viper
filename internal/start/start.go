// Package start creates or reuses a machine and set it up. The wording of the lines is
// that of the script; the programs that run on the server are the script's (see scripts/).
package start

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pt9912/hetzner-viper/internal/config"
	"github.com/pt9912/hetzner-viper/internal/hetzner"
	"github.com/pt9912/hetzner-viper/internal/machine"
	"github.com/pt9912/hetzner-viper/internal/preflight"
	"github.com/pt9912/hetzner-viper/internal/runlog"
)

// ErrShown is returned when a run failed after its reason was shown: it is logged as FAILED.
var ErrShown = errors.New("failed")

// Deps are the outside world of the command.
type Deps struct {
	// Preflight runs the read-only checks, printing to out and errOut; it returns the number of problems.
	Preflight func(ctx context.Context, repoArg string, out, errOut io.Writer) int
	Hetzner   interface {
		ServerByName(ctx context.Context, name string) (*hetzner.Server, error)
		SSHKeys(ctx context.Context) ([]hetzner.SSHKey, error)
		SSHKeyCreate(ctx context.Context, name, publicKey string) error
		FirewallRules(ctx context.Context, name string) ([]hetzner.FirewallRule, bool, error)
		FirewallCreate(ctx context.Context, name string) error
		FirewallAddRule(ctx context.Context, name string, r hetzner.FirewallRule) error
		ServerCreate(ctx context.Context, s hetzner.ServerSpec) (*hetzner.Server, error)
	}
	DNS interface {
		Set(ctx context.Context, host, ip string) (string, error)
	}
	// Run runs a program and returns what it printed (stdin may be empty).
	Run func(ctx context.Context, stdin, name string, args ...string) (stdout, stderr string, err error)
	// Stream runs a program with its output going to the user as it comes.
	Stream func(ctx context.Context, stdin string, stdout, stderr io.Writer, name string, args ...string) error
	// Keygen runs ssh-keygen (host key of a new server, signed by the host CA).
	Keygen         func(name string, args ...string) error
	WriteSSHConfig func(ip string) error
	SSHInclude     func(action string) error
	Sleep          func(time.Duration)
	Now            func() time.Time
	// OpenVSCode opens the work directory in VS Code, if it is installed; errors are ignored.
	OpenVSCode func(alias, workdir string)
}

type runner struct {
	c      *config.Config
	d      Deps
	lg     *runlog.Log
	out    io.Writer
	errOut io.Writer
	repo   string
}

// interrupted reports that the run was cancelled (Ctrl-C): nothing more is started, and failures
// that follow from the cancellation are not shown as problems.
func interrupted(ctx context.Context) bool { return ctx.Err() != nil }

// warnf is a warning of a step that is not fatal; it stays quiet after an interruption.
func (r *runner) warnf(ctx context.Context, format string, a ...any) {
	if !interrupted(ctx) {
		r.eprintf(format, a...)
	}
}

func (r *runner) printf(format string, a ...any)  { fmt.Fprintf(r.out, format, a...) }
func (r *runner) eprintf(format string, a ...any) { fmt.Fprintf(r.errOut, format, a...) }

// ssh runs a command on the server with the machine's own ssh config, output going to the user.
// Without stdin it passes -n (ssh must not read ours).
func (r *runner) ssh(ctx context.Context, stdin, command string) error {
	args := []string{"-F", r.c.SSHConfig, r.c.SSHAlias, command}
	if stdin == "" {
		args = append([]string{"-n"}, args...)
	}
	return r.d.Stream(ctx, stdin, r.out, r.errOut, "ssh", args...)
}

// script runs one of the scripts on the server, with its arguments quoted.
func (r *runner) script(ctx context.Context, script string, args ...string) error {
	return r.ssh(ctx, script, "bash -s -- "+machine.QuoteAll(args...))
}

func readTrim(p string) string {
	b, _ := os.ReadFile(p)
	return strings.TrimRight(string(b), "\n")
}

func (r *runner) fail(format string, a ...any) (int, error) {
	r.eprintf(format+"\n", a...)
	return 1, ErrShown
}

// Run creates or reuses the machine and sets it up. It returns the exit code; a non-nil error is a
// run that failed (ErrShown: the reason was already shown) and is to be logged as FAILED.
func Run(ctx context.Context, c *config.Config, repoArg string, d Deps, lg *runlog.Log, out, errOut io.Writer) (int, error) {
	r := &runner{c: c, d: d, lg: lg, out: out, errOut: errOut}
	repoURL, remembered := preflight.ResolveRepoURL(c, repoArg)
	r.repo = repoURL
	shown := ""
	if repoURL != "" {
		shown = ", " + preflight.RedactURL(repoURL)
	}
	lg.Event("start (%s, %s, %s%s)", c.ServerType, c.ServerLocation, c.ServerImage, shown)

	if code, err := r.preflightChecks(ctx, repoArg); code != 0 || err != nil {
		return code, err
	}
	ca := filepath.Join(c.SSHDir, "hetzner_claude_host_ca")
	deployKey, ipadKey, code, err := r.localFiles(ca)
	if code != 0 || err != nil {
		return code, err
	}
	srv, reused, code, err := r.ensureServer(ctx, ca, ipadKey)
	if code != 0 || err != nil {
		return code, err
	}
	id := fmtID(srv.ID)
	if code, err := r.announceServer(ctx, srv.IPv4); code != 0 || err != nil {
		return code, err
	}
	status, code, err := r.waitForServer(ctx, id, reused)
	if code != 0 || err != nil {
		return code, err
	}
	if code, err := r.pinHostKey(ctx, id, srv.IPv4); code != 0 || err != nil {
		return code, err
	}
	return r.provision(ctx, status, id, repoURL, remembered, deployKey)
}

// preflightChecks runs the read-only checks first, so a wrong setting fails before anything is created.
func (r *runner) preflightChecks(ctx context.Context, repoArg string) (int, error) {
	var pre strings.Builder
	if r.d.Preflight(ctx, repoArg, &pre, &pre) > 0 {
		fmt.Fprint(r.errOut, pre.String())
		return 1, ErrShown
	}
	fmt.Fprint(r.out, pre.String())
	return 0, nil
}

// localFiles checks the local key files and returns the deploy key and iPad key paths.
func (r *runner) localFiles(ca string) (deployKey, ipadKey string, code int, err error) {
	c := r.c
	if !exists(ca) || !exists(c.SSHKey+".pub") {
		code, err = r.fail("Run viper setup first")
		return "", "", code, err
	}
	home := filepath.Dir(c.SSHDir)
	deployKey = config.ExpandHome(c.Get("GIT_DEPLOY_KEY_FILE"), home)
	ipadKey = config.ExpandHome(c.Get("IPAD_SSH_PUBLIC_KEY_FILE"), home)
	for _, f := range []string{deployKey, ipadKey} {
		if f == "" {
			continue
		}
		if _, rerr := os.ReadFile(f); rerr != nil {
			code, err = r.fail("File not readable: %s", f)
			return "", "", code, err
		}
	}
	return deployKey, ipadKey, 0, nil
}

// ensureServer makes sure the login key, the firewall and the server exist.
func (r *runner) ensureServer(ctx context.Context, ca, ipadKey string) (srv *hetzner.Server, reused bool, code int, err error) {
	keyName, code, err := r.loginKey(ctx)
	if code != 0 || err != nil {
		return nil, false, code, err
	}
	if code, err := r.firewall(ctx); code != 0 || err != nil {
		return nil, false, code, err
	}
	return r.server(ctx, ca, keyName, ipadKey)
}

// announceServer records the IP, writes the SSH config and sets the DNS name.
func (r *runner) announceServer(ctx context.Context, ip string) (int, error) {
	c, d := r.c, r.d
	_ = os.WriteFile(c.IPState, []byte(ip+"\n"), 0o600)
	r.lg.Event("ip=%s dns=%s", ip, c.DNSHost)
	if err := d.WriteSSHConfig(ip); err != nil {
		return 1, err
	}
	if err := d.SSHInclude("add"); err != nil {
		r.eprintf("%s\n", machine.SSHIncludeWarning(c))
	}
	// The DNS name is for people (iPad, other clients); the scripts connect by IP and need not wait for it.
	msg, err := d.DNS.Set(ctx, c.DNSHost, ip)
	if err != nil {
		return r.fail("%s", err)
	}
	r.printf("%s\n", msg)
	return 0, nil
}

// pinHostKey pins the server's host key if there is no pin yet.
func (r *runner) pinHostKey(ctx context.Context, id, ip string) (int, error) {
	c, d := r.c, r.d
	// Verified via the host CA (no pin yet): pin the key from now on.
	if st, err := os.Stat(c.KnownPin); err != nil || st.Size() == 0 {
		machine.PinFromServer(c, id, func(args ...string) (string, error) {
			stdout, _, err := d.Run(ctx, "", "ssh", args...)
			return stdout, err
		}, r.out)
		if err := d.WriteSSHConfig(ip); err != nil {
			return 1, err
		}
	}
	return 0, nil
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }
