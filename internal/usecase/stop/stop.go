// Package stop deletes a machine safely. Before the server is destroyed, the git
// repositories, loose files and sessions on it are checked and unsaved work needs an explicit
// DISCARD; the deletion itself needs DELETE. The wording is that of the script.
package stop

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
	"github.com/pt9912/hetzner-viper/internal/port/driven/dns"
	"github.com/pt9912/hetzner-viper/internal/port/driven/runlog"
	"github.com/pt9912/hetzner-viper/internal/port/driven/sshconfig"
	"github.com/pt9912/hetzner-viper/internal/port/driven/system"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

// remote runs on the server and reports what would be lost; it changes nothing there.
//
//go:embed remote.sh
var remote string

// Deps are the outside world of the command.
type Deps struct {
	Cloud interface {
		cloud.Describer
		ServerByName(ctx context.Context, name string) (*model.Server, error)
		DeleteServer(ctx context.Context, id string) error
	}
	DNS dns.Deleter
	// Run runs a program; stdin may be empty.
	Run system.Runner
	// Ask shows a prompt and returns the answer line ("" at the end of the input).
	Ask func(prompt string) string
	SSH sshconfig.Store
}

func readTrim(path string) string {
	b, _ := os.ReadFile(path)
	return strings.TrimRight(string(b), "\n")
}

func (d Deps) cleanupLocal(c *model.Machine, errOut io.Writer) {
	// Remove the local SSH config of this machine.
	if err := d.SSH.RemoveInclude(c); err != nil {
		fmt.Fprintln(errOut, d.SSH.IncludeWarning(c))
	}
	for _, f := range stateFiles(c) {
		_ = os.Remove(f) // best effort: may not exist
	}
}

func stateFiles(c *model.Machine) []string {
	return []string{c.SSHConfig, c.KnownPin, c.IPState, c.IDState, c.WorkspaceState, c.ReadyState, c.RepoState}
}

func (d Deps) deleteDNS(ctx context.Context, c *model.Machine, ip string, out, errOut io.Writer) {
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
func Run(ctx context.Context, c *model.Machine, force bool, d Deps, lg runlog.Log, out, errOut io.Writer) (int, error) {
	srv, err := d.Cloud.ServerByName(ctx, c.ServerName)
	if err != nil {
		fmt.Fprintf(errOut, "%s: listing the servers failed (check %s): %s\n", d.Cloud.Name(), d.Cloud.Hint(), d.Cloud.Describe(err))
		return 1, driving.ErrShown
	}
	if srv == nil {
		return notFound(ctx, c, d, lg, out, errOut), nil
	}
	ip, id := srv.IPv4, srv.ID
	d.SSH.ForgetStalePin(c, id, out)
	if err := d.SSH.WriteConfig(c, ip); err != nil {
		return 1, err
	}
	wss := workspaces(c)
	skip, code, err := probeServer(ctx, c, force, d, id, ip, out, errOut)
	if code != 0 || err != nil {
		return code, err
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
	return destroy(ctx, c, d, lg, srv, skip, out, errOut)
}

// workspaces are the directories the server was started with (start records each), else the configured one.
func workspaces(c *model.Machine) []string {
	// Check the directory the server was started with, even if WORKSPACE_DIR changed since.
	var wss []string
	for _, l := range strings.Split(readTrim(c.WorkspaceState), "\n") {
		if l != "" {
			wss = append(wss, l)
		}
	}
	if len(wss) == 0 {
		wss = []string{c.WorkspaceDir}
	}
	return wss
}

// probeServer checks that ssh works. It returns skip=true if --force skips the git checks after a
// failure; code != 0 or err != nil ends the run.
func probeServer(ctx context.Context, c *model.Machine, force bool, d Deps, id, ip string, out, errOut io.Writer) (skip bool, code int, err error) {
	// -n: ssh must not swallow the answers that follow on stdin. The git checks run whenever
	// the server is reachable and trusted, with or without --force.
	_, sshErr, err := d.Run(ctx, "", "ssh", "-n", "-F", c.SSHConfig, "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", c.SSHAlias, "true")
	if err != nil {
		explainSSHFailure(c, strings.TrimRight(sshErr, "\n"), errOut)
		if !force {
			return false, 1, driving.ErrShown
		}
		fmt.Fprintln(errOut, "WARNING: --force skips the git checks; everything on the server is lost.")
		return true, 0, nil
	}
	if st, err := os.Stat(c.KnownPin); err != nil || st.Size() == 0 {
		d.SSH.PinHostKey(c, id, func(args ...string) (string, error) {
			stdout, _, err := d.Run(ctx, "", "ssh", args...)
			return stdout, err
		}, out)
		if err := d.SSH.WriteConfig(c, ip); err != nil {
			return false, 1, err
		}
	}
	return false, 0, nil
}

// explainSSHFailure says why the server cannot be checked.
func explainSSHFailure(c *model.Machine, e string, errOut io.Writer) {
	forceHint := fmt.Sprintf("Unsaved work on it cannot be checked. Only if you are sure it holds nothing of value: viper down %s --force", c.Name)
	switch model.SSHFailureKind(e) {
	case "hostkey":
		fmt.Fprintf(errOut, "The server answers, but its host key is not trusted: %s\n", e)
		fmt.Fprintln(errOut, "It may have been recreated from another computer (stale pin) or its certificate expired without a pin.")
		fmt.Fprintln(errOut, forceHint)
	case "auth":
		fmt.Fprintf(errOut, "The server answers, but rejects the login key %s: %s\n", c.SSHKey, e)
		fmt.Fprintln(errOut, "It was probably created with another login key (another computer or SSH_KEY). Use that one to check and push its work.")
		fmt.Fprintln(errOut, forceHint)
	default:
		fmt.Fprintf(errOut, "SSH unreachable. Use viper down %s --force to delete without checks.\n", c.Name)
	}
}

// destroy deletes the confirmed server, its DNS record and the local state.
func destroy(ctx context.Context, c *model.Machine, d Deps, lg runlog.Log, srv *model.Server, skip bool, out, errOut io.Writer) (int, error) {
	ip, id := srv.IPv4, srv.ID
	if err := d.Cloud.DeleteServer(ctx, srv.ID); err != nil {
		fmt.Fprintf(errOut, "deleting server %s failed: %s\n", c.ServerName, d.Cloud.Describe(err))
		return 1, driving.ErrShown
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

// notFound: possibly the wrong cloud project (token): touch nothing without confirmation, and
// delete DNS only if it still points to the IP this machine had.
func notFound(ctx context.Context, c *model.Machine, d Deps, lg runlog.Log, out, errOut io.Writer) int {
	fmt.Fprintf(out, "Server %s not found in this %s project.\n", c.ServerName, d.Cloud.Name())
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
func checkServer(ctx context.Context, c *model.Machine, d Deps, lg runlog.Log, wss []string, out, errOut io.Writer) (int, error) {
	fmt.Fprintln(out, "Checking git repositories, loose files and running sessions ...")
	// No `set -e` remotely: a git error must be reported as unsaved work, not abort the check.
	stdout, stderr, err := d.Run(ctx, remote, "ssh", "-F", c.SSHConfig, c.SSHAlias, "bash -s -- "+model.QuoteAll(wss...))
	// what ssh and the remote commands (git, find) complained about stays visible
	if stderr != "" {
		fmt.Fprint(errOut, stderr)
		if !strings.HasSuffix(stderr, "\n") {
			fmt.Fprintln(errOut)
		}
	}
	if err != nil {
		fmt.Fprintln(errOut, "Git check on the server failed. NOT deleting server (use --force to skip checks).")
		return 1, driving.ErrShown
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

// Service is the use case Down over its dependencies; it implements driving.Down.
type Service struct{ Deps Deps }

// Down deletes the machine after the checks; see Run.
func (s Service) Down(ctx context.Context, c *model.Machine, force bool, lg runlog.Log, out, errOut io.Writer) (int, error) {
	return Run(ctx, c, force, s.Deps, lg, out, errOut)
}
