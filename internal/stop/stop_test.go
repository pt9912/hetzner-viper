package stop_test

import (
	"bytes"
	"context"
	"errors"
	"github.com/pt9912/hetzner-viper/internal/stop"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/config"
	"github.com/pt9912/hetzner-viper/internal/hetzner"
	"github.com/pt9912/hetzner-viper/internal/machine"
	"github.com/pt9912/hetzner-viper/internal/runlog"
)

const cleanReport = "== /Development ==\n== tmux sessions ==\n== end of check ==\n"
const unsavedReport = "== /Development/r ==\n M file.txt\n== tmux sessions ==\nUNSAVED WORK: uncommitted, unpushed, stashed or unversioned data exists and will be lost.\n== end of check ==\n"

type world struct {
	t            *testing.T
	c            *config.Config
	lg           *runlog.Log
	server       *hetzner.Server
	listErr      error
	deleteErr    error
	deleted      []int64
	dnsErr       error
	dnsCalls     []string
	sshErr       string // stderr of the reachability check; "" = reachable
	report       string
	reportErr    error
	reportStderr string
	hostKey      string
	answers      []string
	prompts      []string
	calls        [][]string
	stdins       []string
	includes     []string
	includeErr   error
	out, errb    bytes.Buffer
	state        string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	home, dir := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\nWORKSPACE_DIR=/Development\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(dir, "web", config.Env{Home: home, Lookup: func(string) (string, bool) { return "", false }}, false)
	if err != nil {
		t.Fatal(err)
	}
	lg, err := runlog.Open(c.StateDir, c.ServerName, "down", ioDiscard{}, ioDiscard{})
	if err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, c: c, lg: lg, server: &hetzner.Server{ID: 42, Name: "claude-web", IPv4: "203.0.113.7"}, report: cleanReport,
		hostKey: "ssh-ed25519 AAAAhostkey root@server\n", state: c.StateDir}
	return w
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }

func (w *world) local(files ...string) {
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			w.t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("x\n"), 0o600); err != nil {
			w.t.Fatal(err)
		}
	}
}

func (w *world) allLocal() {
	w.local(w.c.SSHConfig, w.c.KnownPin, w.c.IDState, w.c.WorkspaceState, w.c.ReadyState, w.c.RepoState)
	if err := os.WriteFile(w.c.IDState, []byte("42\n"), 0o600); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(w.c.WorkspaceState, []byte("/Development\n"), 0o600); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(w.c.IPState, []byte("203.0.113.7\n"), 0o600); err != nil {
		w.t.Fatal(err)
	}
}

type hz struct{ w *world }

func (h hz) ServerByName(context.Context, string) (*hetzner.Server, error) {
	return h.w.server, h.w.listErr
}
func (h hz) DeleteServer(_ context.Context, id int64) error {
	h.w.deleted = append(h.w.deleted, id)
	return h.w.deleteErr
}

type dnsFake struct{ w *world }

func (d dnsFake) Delete(_ context.Context, host, ip string) (string, error) {
	d.w.dnsCalls = append(d.w.dnsCalls, host+" "+ip)
	if d.w.dnsErr != nil {
		return "", d.w.dnsErr
	}
	return "DNS record removed (if present)", nil
}

func (w *world) deps() stop.Deps {
	return stop.Deps{
		Hetzner: hz{w}, DNS: dnsFake{w},
		Run: func(_ context.Context, stdin, name string, args ...string) (string, string, error) {
			w.calls = append(w.calls, append([]string{name}, args...))
			w.stdins = append(w.stdins, stdin)
			last := args[len(args)-1]
			switch {
			case last == "true":
				if w.sshErr != "" {
					return "", w.sshErr + "\n", errors.New("exit 255")
				}
				return "", "", nil
			case strings.HasPrefix(last, "cat /etc/ssh"):
				return w.hostKey, "", nil
			case strings.HasPrefix(last, "bash -s -- "):
				return w.report, w.reportStderr, w.reportErr
			}
			return "", "", errors.New("unexpected call")
		},
		Ask: func(p string) string {
			w.prompts = append(w.prompts, p)
			if len(w.answers) == 0 {
				return ""
			}
			a := w.answers[0]
			w.answers = w.answers[1:]
			return a
		},
		WriteSSHConfig: func(ip string) error { return machine.WriteSSHConfig(w.c, ip) },
		SSHInclude: func(action string) error {
			w.includes = append(w.includes, action)
			return w.includeErr
		},
	}
}

func (w *world) run(force bool, answers ...string) (int, error) {
	w.t.Helper()
	w.answers = answers
	code, err := stop.Run(context.Background(), w.c, force, w.deps(), w.lg, &w.out, &w.errb)
	w.lg.Close(err, code)
	return code, err
}

func (w *world) events() string {
	b, _ := os.ReadFile(filepath.Join(w.state, "events.log"))
	return string(b)
}

func (w *world) all() string { return w.out.String() + w.errb.String() }

func has(t *testing.T, s string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(s, p) {
			t.Errorf("missing %q in\n%s", p, s)
		}
	}
}

func gone(t *testing.T, w *world, files ...string) {
	t.Helper()
	for _, f := range files {
		if _, err := os.Lstat(f); err == nil {
			t.Errorf("%s still exists", f)
		}
	}
}

func TestDeletesServerAndDnsAfterDELETE(t *testing.T) {
	w := newWorld(t)
	w.allLocal()
	code, err := w.run(false, "DELETE")
	if code != 0 || err != nil {
		t.Fatalf("%d %v\n%s", code, err, w.all())
	}
	if len(w.deleted) != 1 || w.deleted[0] != 42 || len(w.dnsCalls) != 1 || w.dnsCalls[0] != "claude-web.example.com 203.0.113.7" {
		t.Errorf("deleted %v, dns %q", w.deleted, w.dnsCalls)
	}
	has(t, w.all(), "Checking git repositories, loose files and running sessions ...", "Server claude-web (203.0.113.7) and ALL its data (including /Development) will be DESTROYED.",
		"DNS record removed (if present)", "Server claude-web deleted.")
	if strings.Join(w.prompts, "|") != "Type DELETE to continue: " {
		t.Errorf("a clean machine asks only DELETE: %q", w.prompts)
	}
	gone(t, w, w.c.SSHConfig, w.c.KnownPin, w.c.IPState, w.c.IDState, w.c.WorkspaceState, w.c.ReadyState, w.c.RepoState)
	if strings.Join(w.includes, ",") != "remove" {
		t.Errorf("includes %v", w.includes)
	}
	ev := w.events()
	has(t, ev, "server deleted id=42 ip=203.0.113.7\n", " down      deleted\n")
	if strings.Contains(ev, "FAILED") {
		t.Errorf("events %s", ev)
	}
}

func TestWithoutDELETENothingHappens(t *testing.T) {
	w := newWorld(t)
	w.allLocal()
	for _, answer := range []string{"", "delete", "yes", "DELETE "} {
		w.out.Reset()
		code, _ := w.run(false, answer)
		if code != 1 || len(w.deleted) != 0 || len(w.dnsCalls) != 0 {
			t.Fatalf("%q: code %d, deleted %v", answer, code, w.deleted)
		}
		has(t, w.out.String(), "Cancelled")
	}
	if _, err := os.Stat(w.c.IDState); err != nil {
		t.Error("local files were removed")
	}
	has(t, w.events(), "cancelled (no DELETE)")
}

func TestUnsavedWorkNeedsDISCARDandDELETE(t *testing.T) {
	w := newWorld(t)
	w.allLocal()
	w.report = unsavedReport
	code, _ := w.run(false, "no")
	if code != 1 || len(w.deleted) != 0 {
		t.Fatalf("%d %v", code, w.deleted)
	}
	has(t, w.all(), " M file.txt", "UNSAVED WORK", "Cancelled: commit and push first")
	has(t, w.events(), "cancelled (unsaved work)")
	w.out.Reset()
	w.prompts = nil
	code, err := w.run(false, "DISCARD", "DELETE")
	if code != 0 || err != nil || len(w.deleted) != 1 {
		t.Fatalf("%d %v %v", code, err, w.deleted)
	}
	if strings.Join(w.prompts, "|") != "Unsaved work will be lost. Type DISCARD to continue: |Type DELETE to continue: " {
		t.Errorf("prompts %q", w.prompts)
	}
	// DISCARD alone deletes nothing
	w2 := newWorld(t)
	w2.report = unsavedReport
	if code, _ := w2.run(false, "DISCARD", "no"); code != 1 || len(w2.deleted) != 0 {
		t.Errorf("DISCARD without DELETE: %d %v", code, w2.deleted)
	}
}

func TestAnIncompleteCheckRefuses(t *testing.T) {
	w := newWorld(t)
	w.report = "== /Development ==\n"
	code, err := w.run(false, "DELETE")
	if code != 1 || err != nil || len(w.deleted) != 0 || len(w.prompts) != 0 {
		t.Fatalf("%d %v %v %q", code, err, w.deleted, w.prompts)
	}
	has(t, w.all(), "The git check on the server did not complete. NOT deleting server (use --force to skip checks).")
	has(t, w.events(), "refused (git check incomplete)")
	// a line that merely contains the marker is not the marker
	w = newWorld(t)
	w.report = "== /Development/x == end of check ==\n"
	if code, _ := w.run(false, "DELETE"); code != 1 || len(w.deleted) != 0 {
		t.Errorf("%d", code)
	}
}

func TestTheErrorsOfTheServerCheckStayVisible(t *testing.T) {
	w := newWorld(t)
	w.reportStderr = "fatal: detected dubious ownership in repository at '/Development/r'\n"
	_, _ = w.run(false, "DELETE")
	has(t, w.errb.String(), "fatal: detected dubious ownership in repository at '/Development/r'\n")
	w = newWorld(t)
	w.reportStderr = "Connection to 203.0.113.7 closed by remote host."
	w.reportErr = errors.New("exit 255")
	_, _ = w.run(false, "DELETE")
	has(t, w.errb.String(), "Connection to 203.0.113.7 closed by remote host.\nGit check on the server failed.")
}

func TestAServerWithoutIPv4LeavesTheDnsRecord(t *testing.T) {
	w := newWorld(t)
	w.server.IPv4 = ""
	w.sshErr = "Connection timed out"
	if code, _ := w.run(true, "DELETE"); code != 0 || len(w.deleted) != 1 || len(w.dnsCalls) != 0 {
		t.Fatalf("%d %v %v", code, w.deleted, w.dnsCalls)
	}
	has(t, w.errb.String(), "WARNING: the server had no IPv4 address; DNS record claude-web.example.com left untouched.")
}

func TestAFailingCheckKeepsEverything(t *testing.T) {
	w := newWorld(t)
	w.reportErr = errors.New("exit 255")
	code, err := w.run(false, "DELETE")
	if code != 1 || !errors.Is(err, stop.ErrShown) || len(w.deleted) != 0 {
		t.Fatalf("%d %v", code, err)
	}
	has(t, w.errb.String(), "Git check on the server failed. NOT deleting server (use --force to skip checks).")
	has(t, w.events(), "FAILED (exit 1; see logs/claude-web.log)")
}

func TestUnreachableServerNeedsForce(t *testing.T) {
	w := newWorld(t)
	w.sshErr = "ssh: connect to host 203.0.113.7 port 22: Connection timed out"
	code, err := w.run(false, "DELETE")
	if code != 1 || !errors.Is(err, stop.ErrShown) || len(w.deleted) != 0 {
		t.Fatalf("%d %v", code, err)
	}
	has(t, w.errb.String(), "SSH unreachable. Use viper down web --force to delete without checks.")
	w = newWorld(t)
	w.sshErr = "Connection timed out"
	code, err = w.run(true, "DELETE")
	if code != 0 || err != nil || len(w.deleted) != 1 {
		t.Fatalf("%d %v\n%s", code, err, w.all())
	}
	has(t, w.errb.String(), "WARNING: --force skips the git checks; everything on the server is lost.")
	has(t, w.events(), "server deleted id=42 ip=203.0.113.7 (--force, no git checks)")
	for _, c := range w.calls {
		if strings.HasPrefix(c[len(c)-1], "bash -s") {
			t.Error("--force on an unreachable server must not run the git check")
		}
	}
}

func TestTrustProblemsDoNotRecommendForceBlindly(t *testing.T) {
	w := newWorld(t)
	w.sshErr = "Host key verification failed."
	code, _ := w.run(false, "DELETE")
	got := w.errb.String()
	has(t, got, "The server answers, but its host key is not trusted: Host key verification failed.",
		"It may have been recreated from another computer (stale pin) or its certificate expired without a pin.",
		"Unsaved work on it cannot be checked. Only if you are sure it holds nothing of value: viper down web --force")
	if code != 1 || len(w.deleted) != 0 {
		t.Errorf("%d", code)
	}
	w = newWorld(t)
	w.sshErr = "builder@203.0.113.7: Permission denied (publickey)."
	_, _ = w.run(false, "DELETE")
	has(t, w.errb.String(), "The server answers, but rejects the login key "+w.c.SSHKey+": builder@203.0.113.7: Permission denied (publickey).",
		"It was probably created with another login key", "Only if you are sure it holds nothing of value")
	// --force can still go on
	w = newWorld(t)
	w.sshErr = "Host key verification failed."
	if code, _ := w.run(true, "DELETE"); code != 0 || len(w.deleted) != 1 {
		t.Errorf("force: %d", code)
	}
}

func TestForceStillChecksAReachableServer(t *testing.T) {
	w := newWorld(t)
	w.report = unsavedReport
	if code, _ := w.run(true, "no"); code != 1 || len(w.deleted) != 0 {
		t.Errorf("--force must not skip the checks of a reachable server: %d", code)
	}
	has(t, w.all(), "UNSAVED WORK")
}

func TestCheckCallGetsTheWorkspacesAndTheScript(t *testing.T) {
	w := newWorld(t)
	if err := os.MkdirAll(w.state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.c.WorkspaceState, []byte("/Development\n\n/srv/it's here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _ = w.run(false, "DELETE")
	var cmd, stdin string
	for i, c := range w.calls {
		if strings.HasPrefix(c[len(c)-1], "bash -s -- ") {
			cmd, stdin = c[len(c)-1], w.stdins[i]
		}
	}
	if cmd != `bash -s -- '/Development' '/srv/it'\''s here'` {
		t.Errorf("command %q", cmd)
	}
	if !strings.HasPrefix(stdin, "set -uo pipefail\n") || !strings.Contains(stdin, "== end of check ==") {
		t.Errorf("script %q", stdin[:40])
	}
	has(t, w.errb.String(), "(including /Development /srv/it's here)")
	// no record: the configured workspace
	w = newWorld(t)
	_, _ = w.run(false, "DELETE")
	for _, c := range w.calls {
		if strings.HasPrefix(c[len(c)-1], "bash -s -- ") && c[len(c)-1] != `bash -s -- '/Development'` {
			t.Errorf("command %q", c[len(c)-1])
		}
	}
}

func TestPinsTheHostKeyOfAServerWithoutOne(t *testing.T) {
	w := newWorld(t)
	_, _ = w.run(false, "DELETE")
	has(t, w.out.String(), "Pinned host key of claude-web (verified via host CA).")
	// the stale pin of an earlier server is dropped first
	w = newWorld(t)
	if err := os.MkdirAll(w.state, 0o700); err != nil {
		t.Fatal(err)
	}
	w.local(w.c.KnownPin)
	if err := os.WriteFile(w.c.IDState, []byte("41\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _ = w.run(false, "no")
	has(t, w.out.String(), "Server claude-web was recreated (ID 41 -> 42); dropping its old pinned host key.")
}

func TestDnsFailureStillDeletesWithAWarning(t *testing.T) {
	w := newWorld(t)
	w.dnsErr = errors.New("Cloudflare API error: nope")
	code, err := w.run(false, "DELETE")
	if code != 0 || err != nil || len(w.deleted) != 1 {
		t.Fatalf("%d %v", code, err)
	}
	has(t, w.errb.String(), "Cloudflare API error: nope", "WARNING: DNS removal failed; remove the stale record manually.")
	has(t, w.out.String(), "Server claude-web deleted.")
}

func TestAnHcloudErrorKeepsEverything(t *testing.T) {
	w := newWorld(t)
	w.allLocal()
	w.listErr = errors.New("hcloud: server responded with status code 401")
	code, err := w.run(false, "DELETE")
	if code != 1 || !errors.Is(err, stop.ErrShown) || len(w.deleted) != 0 || len(w.dnsCalls) != 0 {
		t.Fatalf("%d %v", code, err)
	}
	has(t, w.errb.String(), "Hetzner: listing the servers failed (check HCLOUD_TOKEN / project / network): unauthorized (check HCLOUD_TOKEN)")
	if _, err := os.Stat(w.c.IDState); err != nil {
		t.Error("local files were removed")
	}
	has(t, w.events(), "FAILED (exit 1")
	// a failing delete keeps the DNS record and the local files
	w = newWorld(t)
	w.allLocal()
	w.deleteErr = errors.New("boom")
	code, err = w.run(false, "DELETE")
	if code != 1 || err == nil || len(w.dnsCalls) != 0 {
		t.Fatalf("%d %v dns %v", code, err, w.dnsCalls)
	}
	if _, err := os.Stat(w.c.IDState); err != nil {
		t.Error("local files were removed after a failed delete")
	}
}

func TestSshIncludeFailureOnlyWarns(t *testing.T) {
	w := newWorld(t)
	w.includeErr = errors.New("read-only")
	code, _ := w.run(false, "DELETE")
	if code != 0 || len(w.deleted) != 1 {
		t.Fatalf("%d", code)
	}
	has(t, w.errb.String(), "WARNING: could not edit ~/.ssh/config")
}

// --- the server is gone from the project

func TestAbsentServerWithoutLeftoversAsksNothing(t *testing.T) {
	w := newWorld(t)
	w.server = nil
	code, err := w.run(false)
	if code != 0 || err != nil || len(w.prompts) != 0 {
		t.Fatalf("%d %v %q", code, err, w.prompts)
	}
	has(t, w.out.String(), "Server claude-web not found in this Hetzner project.", "Nothing left to clean up.")
	has(t, w.events(), "nothing to clean up (server not found, no local files)")
}

func TestAbsentServerCleansUpOnlyAfterCLEANUP(t *testing.T) {
	w := newWorld(t)
	w.server = nil
	w.allLocal()
	code, _ := w.run(false, "yes")
	if code != 1 || len(w.dnsCalls) != 0 {
		t.Fatalf("%d dns %v", code, w.dnsCalls)
	}
	has(t, w.out.String(), "Leftovers: local SSH config and pinned host key, DNS record claude-web.example.com -> 203.0.113.7.", "Cancelled")
	if _, err := os.Stat(w.c.IDState); err != nil {
		t.Error("files removed without CLEANUP")
	}
	has(t, w.events(), "cancelled (server not found, no CLEANUP)")
	w.out.Reset()
	code, _ = w.run(false, "CLEANUP")
	if code != 0 || len(w.dnsCalls) != 1 || w.dnsCalls[0] != "claude-web.example.com 203.0.113.7" {
		t.Fatalf("%d dns %v", code, w.dnsCalls)
	}
	gone(t, w, w.c.SSHConfig, w.c.KnownPin, w.c.IPState, w.c.IDState, w.c.WorkspaceState, w.c.ReadyState, w.c.RepoState)
	has(t, w.events(), "cleaned up (server not found in the project)")
	if len(w.deleted) != 0 {
		t.Error("nothing to delete")
	}
}

func TestAbsentServerWithoutRecordedIpLeavesDns(t *testing.T) {
	w := newWorld(t)
	w.server = nil
	w.local(w.c.SSHConfig)
	code, _ := w.run(false, "CLEANUP")
	if code != 0 || len(w.dnsCalls) != 0 {
		t.Fatalf("%d dns %v", code, w.dnsCalls)
	}
	has(t, w.out.String(), "Leftovers: local SSH config and pinned host key.\n", "No recorded IP: DNS record claude-web.example.com left untouched.")
	gone(t, w, w.c.SSHConfig)
}

// --- the script on the server is the one of stop.sh, and it finds what it should find

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "init.defaultBranch=main"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// runRemote runs the embedded script like the server would, in a fake home.
func runRemote(t *testing.T, home string, workspaces ...string) string {
	t.Helper()
	cmd := exec.Command("bash", append([]string{"-s", "--"}, workspaces...)...)
	cmd.Stdin = strings.NewReader(stop.Remote())
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "VIPER_TMP_DIRS=" + filepath.Join(home, "no-tmp"), "GIT_CONFIG_GLOBAL=/dev/null"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("remote script: %v\n%s", err, out)
	}
	return string(out)
}

func TestRemoteScriptFindsWhatWouldBeLost(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	home := t.TempDir()
	ws := filepath.Join(home, "ws")
	repo := filepath.Join(ws, "r")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "first")
	git(t, repo, "remote", "add", "origin", "https://example.invalid/r.git")
	// pretend the commit is on the remote
	git(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")

	out := runRemote(t, home, ws)
	if strings.Contains(out, "UNSAVED WORK") || !strings.Contains(out, "== end of check ==") {
		t.Errorf("a pushed, clean repository loses nothing:\n%s", out)
	}
	if err := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := runRemote(t, home, ws); !strings.Contains(out, "?? b.txt") || !strings.Contains(out, "UNSAVED WORK") {
		t.Errorf("untracked file:\n%s", out)
	}
	_ = os.Remove(filepath.Join(repo, "b.txt")) // may not exist
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := runRemote(t, home, ws); !strings.Contains(out, " M a.txt") || !strings.Contains(out, "UNSAVED WORK") {
		t.Errorf("modified file:\n%s", out)
	}
	git(t, repo, "checkout", "-q", "--", "a.txt")
	if err := os.WriteFile(filepath.Join(ws, "loose.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := runRemote(t, home, ws); !strings.Contains(out, "Not in any git repository (lost):") || !strings.Contains(out, "loose.txt") || !strings.Contains(out, "UNSAVED WORK") {
		t.Errorf("loose file:\n%s", out)
	}
	_ = os.Remove(filepath.Join(ws, "loose.txt")) // may not exist
	if err := os.WriteFile(filepath.Join(repo, "c.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "second")
	if out := runRemote(t, home, ws); !strings.Contains(out, "Commits not on any remote") || !strings.Contains(out, "second") {
		t.Errorf("unpushed commit:\n%s", out)
	}
	git(t, repo, "reset", "-q", "--hard", "HEAD~1")
	if out := runRemote(t, home, filepath.Join(home, "missing")); !strings.Contains(out, "ERROR: workspace") || !strings.Contains(out, "UNSAVED WORK") {
		t.Errorf("missing workspace:\n%s", out)
	}
}
