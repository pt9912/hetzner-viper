// Package stop is stop.sh: delete a machine safely. Before the server is destroyed, the git
// repositories, loose files and sessions on it are checked and unsaved work needs an explicit
// DISCARD; the deletion itself needs DELETE. The wording is that of the script.
package stop

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/config"
	"github.com/pt9912/hetzner-viper/internal/hetzner"
	"github.com/pt9912/hetzner-viper/internal/machine"
	"github.com/pt9912/hetzner-viper/internal/runlog"
)

// remote runs on the server and reports what would be lost; it changes nothing there.
//
//go:embed remote.sh
var remote string

// ErrShown is returned when a run failed after its reason was shown: the run is logged as FAILED.
var ErrShown = errors.New("failed")

// Deps are the outside world of the command.
type Deps struct {
	Hetzner interface {
		ServerByName(ctx context.Context, name string) (*hetzner.Server, error)
		DeleteServer(ctx context.Context, id int64) error
	}
	DNS interface {
		Delete(ctx context.Context, host, expectedIP string) (string, error)
	}
	// Run runs a program; stdin may be empty.
	Run func(ctx context.Context, stdin, name string, args ...string) (stdout, stderr string, err error)
	// Ask shows a prompt and returns the answer line ("" at the end of the input).
	Ask            func(prompt string) string
	WriteSSHConfig func(ip string) error
	SSHInclude     func(action string) error
}

func readTrim(path string) string {
	b, _ := os.ReadFile(path)
	return strings.TrimRight(string(b), "\n")
}

func (d Deps) cleanupLocal(c *config.Config, errOut io.Writer) {
	// Remove the local SSH config of this machine.
	if err := d.SSHInclude("remove"); err != nil {
		fmt.Fprintln(errOut, machine.SSHIncludeWarning(c))
	}
	for _, f := range stateFiles(c) {
		os.Remove(f)
	}
}

func stateFiles(c *config.Config) []string {
	return []string{c.SSHConfig, c.KnownPin, c.IPState, c.IDState, c.WorkspaceState, c.ReadyState, c.RepoState}
}

func (d Deps) deleteDNS(ctx context.Context, c *config.Config, ip string, out, errOut io.Writer) {
	msg, err := d.DNS.Delete(ctx, c.DNSHost, ip)
	if err != nil {
		fmt.Fprintln(errOut, err)
		fmt.Fprintln(errOut, "WARNING: DNS removal failed; remove the stale record manually.")
		return
	}
	fmt.Fprintln(out, msg)
}

// Run deletes the machine after the checks. It returns the exit code; a non-nil error is a run
// that failed (ErrShown: the reason was already shown) and is to be logged as FAILED.
func Run(ctx context.Context, c *config.Config, force bool, d Deps, lg *runlog.Log, out, errOut io.Writer) (int, error) {
	srv, err := d.Hetzner.ServerByName(ctx, c.ServerName)
	if err != nil {
		fmt.Fprintf(errOut, "hcloud server list failed (check HCLOUD_TOKEN / project / network): %s\n", hetzner.Error(err))
		return 1, ErrShown
	}
	if srv == nil {
		return notFound(ctx, c, d, lg, out, errOut), nil
	}
	ip, id := srv.IPv4, strconv.FormatInt(srv.ID, 10)
	machine.ForgetStalePin(c, id, out)
	if err := d.WriteSSHConfig(ip); err != nil {
		return 1, err
	}
	// Check the directory the server was started with, even if WORKSPACE_DIR changed since:
	// every workspace the server was started with (start records each), else the configured one.
	var wss []string
	for _, l := range strings.Split(readTrim(c.WorkspaceState), "\n") {
		if l != "" {
			wss = append(wss, l)
		}
	}
	if len(wss) == 0 {
		wss = []string{c.WorkspaceDir}
	}

	// -n: ssh must not swallow the answers that follow on stdin. The git checks run whenever
	// the server is reachable and trusted, with or without --force.
	skip := false
	_, sshErr, err := d.Run(ctx, "", "ssh", "-n", "-F", c.SSHConfig, "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", c.SSHAlias, "true")
	if err != nil {
		e := strings.TrimRight(sshErr, "\n")
		forceHint := fmt.Sprintf("Unsaved work on it cannot be checked. Only if you are sure it holds nothing of value: viper down %s --force", c.Machine)
		switch machine.SSHFailureKind(e) {
		case "hostkey":
			fmt.Fprintf(errOut, "The server answers, but its host key is not trusted: %s\n", e)
			fmt.Fprintln(errOut, "It may have been recreated from another computer (stale pin) or its certificate expired without a pin.")
			fmt.Fprintln(errOut, forceHint)
		case "auth":
			fmt.Fprintf(errOut, "The server answers, but rejects the login key %s: %s\n", c.SSHKey, e)
			fmt.Fprintln(errOut, "It was probably created with another login key (another computer or SSH_KEY). Use that one to check and push its work.")
			fmt.Fprintln(errOut, forceHint)
		default:
			fmt.Fprintf(errOut, "SSH unreachable. Use viper down %s --force to delete without checks.\n", c.Machine)
		}
		if !force {
			return 1, ErrShown
		}
		fmt.Fprintln(errOut, "WARNING: --force skips the git checks; everything on the server is lost.")
		skip = true
	} else if st, err := os.Stat(c.KnownPin); err != nil || st.Size() == 0 {
		machine.PinFromServer(c, id, func(args ...string) (string, error) {
			stdout, _, err := d.Run(ctx, "", "ssh", args...)
			return stdout, err
		}, out)
		if err := d.WriteSSHConfig(ip); err != nil {
			return 1, err
		}
	}
	if !skip {
		if code, err := checkServer(ctx, c, d, lg, wss, out, errOut); code != 0 || err != nil {
			return code, err
		}
	}
	fmt.Fprintf(errOut, "\nServer %s (%s) and ALL its data (including %s) will be DESTROYED.\n", c.ServerName, ip, strings.Join(wss, " "))
	if d.Ask("Type DELETE to continue: ") != "DELETE" {
		fmt.Fprintln(out, "Cancelled")
		lg.Final("cancelled (no DELETE)")
		return 1, nil
	}
	if err := d.Hetzner.DeleteServer(ctx, srv.ID); err != nil {
		fmt.Fprintf(errOut, "deleting server %s failed: %s\n", c.ServerName, hetzner.Error(err))
		return 1, ErrShown
	}
	forced := ""
	if skip {
		forced = " (--force, no git checks)"
	}
	lg.Event("server deleted id=%s ip=%s%s", id, ip, forced)
	if ip == "" {
		// without the IP the record cannot be matched to this server: leave it
		fmt.Fprintf(errOut, "WARNING: the server had no IPv4 address; DNS record %s left untouched.\n", c.DNSHost)
	} else {
		d.deleteDNS(ctx, c, ip, out, errOut)
	}
	d.cleanupLocal(c, errOut)
	lg.Final("deleted")
	fmt.Fprintf(out, "Server %s deleted.\n", c.ServerName)
	return 0, nil
}

// notFound: possibly the wrong Hetzner project (token): touch nothing without confirmation, and
// delete DNS only if it still points to the IP this machine had.
func notFound(ctx context.Context, c *config.Config, d Deps, lg *runlog.Log, out, errOut io.Writer) int {
	fmt.Fprintf(out, "Server %s not found in this Hetzner project.\n", c.ServerName)
	oldIP := readTrim(c.IPState)
	left := false
	for _, f := range stateFiles(c) {
		if _, err := os.Lstat(f); err == nil {
			left = true
		}
	}
	if !left {
		fmt.Fprintln(out, "Nothing left to clean up.")
		lg.Final("nothing to clean up (server not found, no local files)")
		return 0
	}
	dnsPart := ""
	if oldIP != "" {
		dnsPart = fmt.Sprintf(", DNS record %s -> %s", c.DNSHost, oldIP)
	}
	fmt.Fprintf(out, "Leftovers: local SSH config and pinned host key%s.\n", dnsPart)
	if d.Ask("Type CLEANUP to remove them: ") != "CLEANUP" {
		fmt.Fprintln(out, "Cancelled")
		lg.Final("cancelled (server not found, no CLEANUP)")
		return 1
	}
	if oldIP != "" {
		d.deleteDNS(ctx, c, oldIP, out, errOut)
	} else {
		fmt.Fprintf(out, "No recorded IP: DNS record %s left untouched.\n", c.DNSHost)
	}
	d.cleanupLocal(c, errOut)
	lg.Final("cleaned up (server not found in the project)")
	return 0
}

var unsavedRe = regexp.MustCompile(`(?m)^UNSAVED WORK`)
var endRe = regexp.MustCompile(`(?m)^== end of check ==$`)

// checkServer asks the server what would be lost. It returns a non-zero code when the deletion
// must not go on.
func checkServer(ctx context.Context, c *config.Config, d Deps, lg *runlog.Log, wss []string, out, errOut io.Writer) (int, error) {
	fmt.Fprintln(out, "Checking git repositories, loose files and running sessions ...")
	// No `set -e` remotely: a git error must be reported as unsaved work, not abort the check.
	stdout, stderr, err := d.Run(ctx, remote, "ssh", "-F", c.SSHConfig, c.SSHAlias, "bash -s -- "+machine.QuoteAll(wss...))
	// what ssh and the remote commands (git, find) complained about stays visible
	if stderr != "" {
		fmt.Fprint(errOut, stderr)
		if !strings.HasSuffix(stderr, "\n") {
			fmt.Fprintln(errOut)
		}
	}
	if err != nil {
		fmt.Fprintln(errOut, "Git check on the server failed. NOT deleting server (use --force to skip checks).")
		return 1, ErrShown
	}
	// report, warning and prompts on one stream (stderr, like read -p), so they stay in order
	report := strings.TrimRight(stdout, "\n")
	fmt.Fprintln(errOut, report)
	if !endRe.MatchString(report) {
		fmt.Fprintln(errOut, "The git check on the server did not complete. NOT deleting server (use --force to skip checks).")
		lg.Final("refused (git check incomplete)")
		return 1, nil
	}
	if unsavedRe.MatchString(report) {
		if d.Ask("Unsaved work will be lost. Type DISCARD to continue: ") != "DISCARD" {
			fmt.Fprintln(out, "Cancelled: commit and push first")
			lg.Final("cancelled (unsaved work)")
			return 1, nil
		}
	}
	return 0, nil
}
