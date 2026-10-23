// Package start is start.sh: create or reuse a machine and set it up. The wording of the lines is
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
	ctx    context.Context
	c      *config.Config
	d      Deps
	lg     *runlog.Log
	out    io.Writer
	errOut io.Writer
	repo   string
}

// interrupted reports that the run was cancelled (Ctrl-C): nothing more is started, and failures
// that follow from the cancellation are not shown as problems.
func (r *runner) interrupted() bool { return r.ctx.Err() != nil }

// warnf is a warning of a step that is not fatal; it stays quiet after an interruption.
func (r *runner) warnf(format string, a ...any) {
	if !r.interrupted() {
		r.eprintf(format, a...)
	}
}

func (r *runner) printf(format string, a ...any)  { fmt.Fprintf(r.out, format, a...) }
func (r *runner) eprintf(format string, a ...any) { fmt.Fprintf(r.errOut, format, a...) }

// ssh runs a command on the server with the machine's own ssh config, output going to the user.
// Without stdin it passes -n (ssh must not read ours).
func (r *runner) ssh(stdin, command string) error {
	args := []string{"-F", r.c.SSHConfig, r.c.SSHAlias, command}
	if stdin == "" {
		args = append([]string{"-n"}, args...)
	}
	return r.d.Stream(r.ctx, stdin, r.out, r.errOut, "ssh", args...)
}

// script runs one of the scripts on the server, with its arguments quoted.
func (r *runner) script(script string, args ...string) error {
	return r.ssh(script, "bash -s -- "+machine.QuoteAll(args...))
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
	r := &runner{ctx: ctx, c: c, d: d, lg: lg, out: out, errOut: errOut}
	repoURL, remembered := preflight.ResolveRepoURL(c, repoArg)
	r.repo = repoURL
	shown := ""
	if repoURL != "" {
		shown = ", " + preflight.RedactURL(repoURL)
	}
	lg.Event("start (%s, %s, %s%s)", c.ServerType, c.ServerLocation, c.ServerImage, shown)

	// read-only checks first, so a wrong setting fails before anything is created
	var pre strings.Builder
	if d.Preflight(ctx, repoArg, &pre, &pre) > 0 {
		fmt.Fprint(errOut, pre.String())
		return 1, ErrShown
	}
	fmt.Fprint(out, pre.String())

	ca := filepath.Join(c.SSHDir, "hetzner_claude_host_ca")
	if !exists(ca) || !exists(c.SSHKey+".pub") {
		return r.fail("Run viper setup first")
	}
	home := filepath.Dir(c.SSHDir)
	deployKey := config.ExpandHome(c.Get("GIT_DEPLOY_KEY_FILE"), home)
	ipadKey := config.ExpandHome(c.Get("IPAD_SSH_PUBLIC_KEY_FILE"), home)
	for _, f := range []string{deployKey, ipadKey} {
		if f == "" {
			continue
		}
		if _, err := os.ReadFile(f); err != nil {
			return r.fail("File not readable: %s", f)
		}
	}

	keyName, code, err := r.loginKey()
	if code != 0 || err != nil {
		return code, err
	}
	if code, err := r.firewall(); code != 0 || err != nil {
		return code, err
	}
	srv, reused, code, err := r.server(ca, keyName, ipadKey)
	if code != 0 || err != nil {
		return code, err
	}
	id := fmtID(srv.ID)
	ip := srv.IPv4
	os.WriteFile(c.IPState, []byte(ip+"\n"), 0o600)
	lg.Event("ip=%s dns=%s", ip, c.DNSHost)
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

	status, code, err := r.waitForServer(id, ip, reused)
	if code != 0 || err != nil {
		return code, err
	}
	// Verified via the host CA (no pin yet): pin the key from now on.
	if st, err := os.Stat(c.KnownPin); err != nil || st.Size() == 0 {
		machine.PinFromServer(c, id, func(args ...string) (string, error) {
			stdout, _, err := d.Run(ctx, "", "ssh", args...)
			return stdout, err
		}, out)
		if err := d.WriteSSHConfig(ip); err != nil {
			return 1, err
		}
	}
	if code, err := r.provision(status, id, repoURL, remembered, deployKey); code != 0 || err != nil {
		return code, err
	}
	return 0, nil
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }
