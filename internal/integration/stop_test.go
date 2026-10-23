package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// boot is a machine that is up.
func boot(t *testing.T) *Env {
	t.Helper()
	e := New(t, "")
	e.MustUp("web", "")
	return e
}

func (e *Env) ws() string { return e.Config("web").WorkspaceDir }

func (e *Env) gitIn(dir string, args ...string) {
	e.t.Helper()
	full := append([]string{"-c", "user.email=t@t", "-c", "user.name=t", "-c", "init.defaultBranch=main"}, args...)
	if out, err := gitCmd(dir, full...); err != nil {
		e.t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

func (e *Env) mkdir(path string) string {
	e.t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		e.t.Fatal(err)
	}
	return path
}

func (e *Env) write(path, content string) {
	e.t.Helper()
	e.mkdir(filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// pushedRepo is a repository with an upstream, everything committed and pushed.
func (e *Env) pushedRepo() string {
	e.t.Helper()
	origin := filepath.Join(e.t.TempDir(), "origin.git")
	proj := filepath.Join(e.ws(), "proj")
	e.gitIn("", "init", "-q", "--bare", origin)
	e.gitIn("", "clone", "-q", origin, proj)
	e.write(filepath.Join(proj, ".gitignore"), "build/\n")
	e.gitIn(proj, "add", ".gitignore")
	e.gitIn(proj, "commit", "-q", "-m", "init")
	e.gitIn(proj, "push", "-q", "-u", "origin", "HEAD")
	e.gitIn(proj, "remote", "set-url", "origin", "https://git.example.invalid/proj.git") // a remote off the server
	return proj
}

func (e *Env) kept() bool { return e.Cloud.Servers["claude-web"] != nil }

func (r Result) HasLine(t *testing.T, line string) {
	t.Helper()
	for _, l := range strings.Split(r.Out, "\n") {
		if l == line {
			return
		}
	}
	t.Errorf("no line %q in\n%s", line, r.Out)
}

func TestStopAbortsWithoutDELETEConfirmation(t *testing.T) {
	e := boot(t)
	r := e.Down("web", false, "no")
	if r.Code != 1 || !e.kept() {
		t.Errorf("code %d, kept %v", r.Code, e.kept())
	}
}

func TestStopDeletesServerAndDns(t *testing.T) {
	e := boot(t)
	r := e.Down("web", false, "DELETE")
	if r.Code != 0 || e.kept() || len(e.Cloud.DNSRecords) != 0 {
		t.Errorf("code %d, kept %v, dns %v\n%s", r.Code, e.kept(), e.Cloud.DNSRecords, r.Out)
	}
}

func TestStopOnlyTouchesTheNamedMachine(t *testing.T) {
	e := boot(t)
	e.MustUp("api", "")
	e.Down("web", false, "DELETE")
	if e.Cloud.Servers["claude-api"] == nil {
		t.Error("api server gone")
	}
	if _, ok := e.Cloud.DNSRecords["claude-api.example.com"]; !ok {
		t.Error("api dns gone")
	}
}

func TestStopAsksAnExtraQuestionForUnsavedWork(t *testing.T) {
	e := boot(t)
	proj := filepath.Join(e.ws(), "proj")
	e.gitIn("", "init", "-q", proj)
	e.write(filepath.Join(proj, "file"), "x\n")
	r := e.Down("web", false, "DISCARD", "no")
	r.HasLine(t, "?? file")
	r.Has(t, "UNSAVED WORK")
	if !e.kept() {
		t.Error("server gone")
	}
}

func TestStopUnsavedWorkCancelledWithoutDISCARD(t *testing.T) {
	e := boot(t)
	proj := filepath.Join(e.ws(), "proj")
	e.gitIn("", "init", "-q", proj)
	e.write(filepath.Join(proj, "file"), "x\n")
	if r := e.Down("web", false, "DELETE", "DELETE"); r.Code != 1 || !e.kept() {
		t.Errorf("code %d, kept %v", r.Code, e.kept())
	}
}

func TestStopUnsavedWorkDeletedAfterDISCARDAndDELETE(t *testing.T) {
	e := boot(t)
	proj := filepath.Join(e.ws(), "proj")
	e.gitIn("", "init", "-q", proj)
	e.write(filepath.Join(proj, "file"), "x\n")
	if r := e.Down("web", false, "DISCARD", "DELETE"); r.Code != 0 || e.kept() {
		t.Errorf("code %d, kept %v\n%s", r.Code, e.kept(), r.Out)
	}
}

func TestStopNoWarningWithoutRepos(t *testing.T) {
	e := boot(t)
	e.Down("web", false, "DELETE").HasNot(t, "UNSAVED WORK")
}

func TestStopUnreachableNeedsForce(t *testing.T) {
	e := boot(t)
	e.Srv.Env["MOCK_SSH_DOWN"] = "1"
	r := e.Down("web", false, "DELETE")
	if r.Code != 1 || !e.kept() {
		t.Errorf("code %d, kept %v", r.Code, e.kept())
	}
	r.Has(t, "--force")
	if r := e.Down("web", true, "DELETE"); r.Code != 0 || e.kept() {
		t.Errorf("force: code %d, kept %v\n%s", r.Code, e.kept(), r.Out)
	}
}

func (e *Env) leftover(name string) string {
	return filepath.Join(e.Home, ".local", "state", "hetzner-claude", name)
}

func TestStopOfAbsentServerAsksBeforeCleanup(t *testing.T) {
	e := New(t, "")
	e.write(e.leftover("ip-claude-ghost"), "10.0.0.9\n")
	if r := e.Down("ghost", false, "no"); r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	if _, err := os.Stat(e.leftover("ip-claude-ghost")); err != nil {
		t.Error("removed without CLEANUP")
	}
	if r := e.Down("ghost", false, "CLEANUP"); r.Code != 0 {
		t.Errorf("code %d\n%s", r.Code, r.Out)
	}
	if _, err := os.Stat(e.leftover("ip-claude-ghost")); err == nil {
		t.Error("not removed with CLEANUP")
	}
}

func TestStopOfAbsentServerWithoutLeftoversDoesNotAsk(t *testing.T) {
	e := New(t, "")
	r := e.Down("ghost", false)
	r.Has(t, "Nothing left to clean up")
	if r.Code != 0 {
		t.Errorf("code %d", r.Code)
	}
}

func TestStopFlagsStashAsUnsaved(t *testing.T) {
	e := boot(t)
	proj := e.pushedRepo()
	e.write(filepath.Join(proj, ".gitignore"), "build/\ny\n")
	e.gitIn(proj, "stash", "-q")
	r := e.Down("web", false, "no")
	r.Has(t, "stash@{0}", "UNSAVED WORK")
}

func TestStopListsIgnoredFilesWithoutBlocking(t *testing.T) {
	e := boot(t)
	proj := e.pushedRepo()
	e.write(filepath.Join(proj, "build", "out"), "x\n")
	r := e.Down("web", false, "DELETE")
	r.Has(t, "Ignored by git", "build/")
	r.HasNot(t, "UNSAVED WORK")
	if e.kept() {
		t.Error("not deleted")
	}
}

func TestStopReportsABrokenRepoAsUnsaved(t *testing.T) {
	e := boot(t)
	e.mkdir(filepath.Join(e.ws(), "broken", ".git"))
	r := e.Down("web", false, "no")
	r.Has(t, "ERROR: cannot read git", "UNSAVED WORK")
	if !e.kept() {
		t.Error("server gone")
	}
}

func TestStopRemovesTheLocalSshConfig(t *testing.T) {
	e := boot(t)
	e.Down("web", false, "DELETE")
	if _, err := os.Stat(filepath.Join(e.Home, ".ssh", "config_hetzner_claude-web")); err == nil {
		t.Error("config file stays")
	}
	b, _ := os.ReadFile(filepath.Join(e.Home, ".ssh", "config"))
	if strings.Contains(string(b), "config_hetzner_claude-web") {
		t.Error("include stays")
	}
}

func TestStopOfAbsentServerCleansUpLeftovers(t *testing.T) {
	e := boot(t)
	delete(e.Cloud.Servers, "claude-web") // deleted in the Hetzner console
	e.Down("web", false, "CLEANUP")
	if len(e.Cloud.DNSRecords) != 0 {
		t.Errorf("dns %v", e.Cloud.DNSRecords)
	}
	if _, err := os.Stat(filepath.Join(e.Home, ".ssh", "config_hetzner_claude-web")); err == nil {
		t.Error("config file stays")
	}
}

func TestStopWrongProjectWithoutConfirmationChangesNothing(t *testing.T) {
	e := boot(t)
	delete(e.Cloud.Servers, "claude-web") // e.g. the token of another project
	if r := e.Down("web", false, "no"); r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	if len(e.Cloud.DNSRecords) != 1 {
		t.Error("dns changed")
	}
	for _, f := range []string{"config_hetzner_claude-web", "known_hosts_hetzner_claude-web"} {
		if _, err := os.Stat(filepath.Join(e.Home, ".ssh", f)); err != nil {
			t.Errorf("%s removed", f)
		}
	}
}

func TestStopAbsentWithoutRecordedIpLeavesDns(t *testing.T) {
	e := boot(t)
	delete(e.Cloud.Servers, "claude-web")
	os.Remove(e.leftover("ip-claude-web"))
	e.Down("web", false, "CLEANUP")
	if len(e.Cloud.DNSRecords) != 1 {
		t.Errorf("dns %v", e.Cloud.DNSRecords)
	}
}

func TestStopFindsReposOutsideTheWorkspace(t *testing.T) {
	e := boot(t)
	scratch := filepath.Join(e.Srv.Home, "scratch")
	e.gitIn("", "init", "-q", scratch)
	e.write(filepath.Join(scratch, "f"), "x\n")
	exp := filepath.Join(e.Srv.State, "remote-tmp", "exp")
	e.gitIn("", "init", "-q", exp)
	r := e.Down("web", false, "no")
	r.Has(t, "== "+scratch+" ==", "== "+exp+" ==", "UNSAVED WORK")
}

func TestStopFlagsLooseFilesInTheWorkspace(t *testing.T) {
	e := boot(t)
	e.write(filepath.Join(e.ws(), "notes.txt"), "notes\n")
	r := e.Down("web", false, "no")
	r.Has(t, "Not in any git repository", "notes.txt", "UNSAVED WORK")
}

func TestStopFlagsAMissingWorkspace(t *testing.T) {
	e := boot(t)
	if err := os.Remove(e.ws()); err != nil {
		t.Fatal(err)
	}
	r := e.Down("web", false, "no")
	r.Has(t, "does not exist", "UNSAVED WORK")
}

func TestStopAnswersFromAPipeReachThePrompts(t *testing.T) {
	e := boot(t)
	proj := e.pushedRepo()
	e.write(filepath.Join(proj, "dirty"), "x\n")
	if r := e.Down("web", false, "DISCARD", "DELETE"); r.Code != 0 || e.kept() {
		t.Errorf("code %d, kept %v", r.Code, e.kept())
	}
}

func TestStopFlagsUnpushedCommitsOnAnotherBranch(t *testing.T) {
	e := boot(t)
	proj := e.pushedRepo()
	e.gitIn(proj, "checkout", "-q", "-b", "feature")
	e.write(filepath.Join(proj, "f"), "f\n")
	e.gitIn(proj, "add", "f")
	e.gitIn(proj, "commit", "-q", "-m", "wip")
	e.gitIn(proj, "checkout", "-q", "-")
	e.Down("web", false, "no").Has(t, "wip", "UNSAVED WORK")
}

func TestStopChecksLinkedWorktrees(t *testing.T) {
	e := boot(t)
	proj := e.pushedRepo()
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	e.gitIn(proj, "worktree", "add", "-q", elsewhere, "-b", "wt")
	e.write(filepath.Join(elsewhere, "new"), "dirty\n")
	e.Down("web", false, "no").Has(t, "== "+elsewhere+" ==", "UNSAVED WORK")
}

func TestStopCleanPushedRepoNeedsNoDISCARD(t *testing.T) {
	e := boot(t)
	e.pushedRepo()
	if r := e.Down("web", false, "DELETE"); r.Code != 0 || e.kept() {
		t.Errorf("code %d, kept %v\n%s", r.Code, e.kept(), r.Out)
	}
}

func TestStopHetznerErrorKeepsEverything(t *testing.T) {
	e := boot(t)
	e.Cloud.ListErr = errBoom
	r := e.Down("web", false, "DELETE")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "list failed")
	if len(e.Cloud.DNSRecords) != 1 {
		t.Error("dns changed")
	}
	if _, err := os.Stat(filepath.Join(e.Home, ".ssh", "config_hetzner_claude-web")); err != nil {
		t.Error("ssh config removed")
	}
}

func TestStopChecksTheWorkspaceFromStart(t *testing.T) {
	e := boot(t)
	proj := filepath.Join(e.ws(), "proj")
	e.gitIn("", "init", "-q", proj)
	e.write(filepath.Join(proj, "file"), "x\n")
	other := e.mkdir(filepath.Join(t.TempDir(), "other"))
	e.WriteSettings("WORKSPACE_DIR=" + other + "\n")
	e.Down("web", false, "no").Has(t, "== "+proj+" ==", "UNSAVED WORK")
}

func TestStopChecksEveryWorkspaceTheServerHad(t *testing.T) {
	e := boot(t)
	mnt := e.ws()
	proj := filepath.Join(mnt, "proj")
	e.gitIn("", "init", "-q", proj)
	e.write(filepath.Join(proj, "file"), "x\n")
	other := e.mkdir(filepath.Join(t.TempDir(), "other"))
	e.WriteSettings("WORKSPACE_DIR=" + other + "\n")
	e.MustUp("web", "") // rerun with a new workspace (e.g. to refresh a token)
	r := e.Down("web", false, "DISCARD", "no")
	r.Has(t, "== "+proj+" ==", "UNSAVED WORK", "including "+mnt+" "+other)
}

func TestNewServerForgetsTheWorkspacesOfAnEarlierOne(t *testing.T) {
	e := boot(t)
	f := e.leftover("workspace-claude-web")
	old, _ := os.ReadFile(f)
	os.WriteFile(f, append(old, []byte(filepath.Join(t.TempDir(), "gone")+"\n")...), 0o600)
	delete(e.Cloud.Servers, "claude-web") // deleted in the console
	e.MustUp("web", "")
	if got := strings.TrimSpace(string(mustRead(t, f))); got != e.ws() {
		t.Errorf("workspaces %q, want only %q", got, e.ws())
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestStopDnsFailureStillDeletesWithAWarning(t *testing.T) {
	e := boot(t)
	e.Cloud.DNSDeleteFail = true
	r := e.Down("web", false, "DELETE")
	if r.Code != 0 || e.kept() {
		t.Errorf("code %d, kept %v", r.Code, e.kept())
	}
	r.Has(t, "DNS removal failed")
}

func TestStopHostKeyRejectionDoesNotRecommendForceBlindly(t *testing.T) {
	e := boot(t)
	e.Srv.Env["MOCK_SSH_HOSTKEY_FAIL"] = "1"
	r := e.Down("web", false, "DELETE")
	r.Has(t, "host key is not trusted")
	if r.Code != 1 || !e.kept() {
		t.Errorf("code %d, kept %v", r.Code, e.kept())
	}
}

func TestStopForceStillChecksAReachableServer(t *testing.T) {
	e := boot(t)
	proj := filepath.Join(e.ws(), "proj")
	e.gitIn("", "init", "-q", proj)
	e.write(filepath.Join(proj, "file"), "x\n")
	r := e.Down("web", true, "no")
	r.Has(t, "UNSAVED WORK")
	if r.Code != 1 || !e.kept() {
		t.Errorf("code %d, kept %v", r.Code, e.kept())
	}
}

func TestStopLoginKeyRejectionDoesNotRecommendForceBlindly(t *testing.T) {
	e := boot(t)
	e.Srv.Env["MOCK_SSH_AUTH_FAIL"] = "1"
	r := e.Down("web", false, "DELETE")
	r.Has(t, "rejects the login key")
	r.HasNot(t, "SSH unreachable")
	if !e.kept() {
		t.Error("server gone")
	}
}

func TestStopChecksWorkspacesRecordedOnTheServer(t *testing.T) {
	e := boot(t)
	srv := e.mkdir(filepath.Join(t.TempDir(), "srv"))
	e.gitIn("", "init", "-q", filepath.Join(srv, "proj"))
	e.write(filepath.Join(srv, "proj", "f"), "x\n")
	f := filepath.Join(e.Srv.Home, ".config", "viper", "workspaces")
	old, _ := os.ReadFile(f)
	os.WriteFile(f, append(old, []byte(srv+"\n")...), 0o644) // started from another computer
	e.Down("web", false, "no").Has(t, "== "+filepath.Join(srv, "proj")+" ==", "UNSAVED WORK")
}

func TestStopFlagsARemoteOnTheServerItself(t *testing.T) {
	e := boot(t)
	backup := filepath.Join(e.Srv.Home, "backup.git")
	e.gitIn("", "init", "-q", "--bare", backup)
	proj := filepath.Join(e.ws(), "proj")
	e.gitIn("", "clone", "-q", backup, proj)
	e.gitIn(proj, "commit", "-q", "--allow-empty", "-m", "x")
	e.gitIn(proj, "push", "-q", "origin", "HEAD")
	e.Down("web", false, "no").Has(t, "Remote origin is on this server", "UNSAVED WORK")
}

func TestStopSeesUntrackedFilesDespiteGitConfig(t *testing.T) {
	e := boot(t)
	proj := e.pushedRepo()
	e.gitIn(proj, "config", "status.showUntrackedFiles", "no")
	e.write(filepath.Join(proj, "new_module.py"), "new\n")
	r := e.Down("web", false, "no")
	r.HasLine(t, "?? new_module.py")
	r.Has(t, "UNSAVED WORK")
}

func TestStopFlagsFilesHiddenByAssumeUnchanged(t *testing.T) {
	e := boot(t)
	proj := e.pushedRepo()
	e.gitIn(proj, "update-index", "--assume-unchanged", ".gitignore")
	e.write(filepath.Join(proj, ".gitignore"), "build/\nchanged\n")
	e.Down("web", false, "no").Has(t, "Hidden from git status", "UNSAVED WORK")
}

func TestStopFlagsCommitsOnlyReachableFromATag(t *testing.T) {
	e := boot(t)
	proj := e.pushedRepo()
	e.gitIn(proj, "checkout", "-q", "--detach")
	e.gitIn(proj, "commit", "-q", "--allow-empty", "-m", "tagged-only")
	e.gitIn(proj, "tag", "v9")
	e.gitIn(proj, "checkout", "-q", "-")
	e.Down("web", false, "no").Has(t, "tagged-only", "UNSAVED WORK")
}

func TestStopRefusesWhenTheCheckDidNotComplete(t *testing.T) {
	e := boot(t)
	e.Srv.Env["MOCK_SSH_SKIP_SCRIPT"] = "1"
	r := e.Down("web", false, "DELETE")
	r.Has(t, "git check on the server did not complete")
	if r.Code != 1 || !e.kept() {
		t.Errorf("code %d, kept %v", r.Code, e.kept())
	}
}

func TestStopDoesNotFlagTagsThatCameWithTheClone(t *testing.T) {
	e := New(t, "")
	up := filepath.Join(t.TempDir(), "up.git")
	seed := filepath.Join(t.TempDir(), "seed")
	e.gitIn("", "init", "-q", "--bare", up)
	e.gitIn("", "clone", "-q", up, seed)
	e.gitIn(seed, "commit", "-q", "--allow-empty", "-m", "main")
	e.gitIn(seed, "push", "-q", "origin", "HEAD:main")
	e.gitIn(seed, "checkout", "-q", "--detach")
	e.gitIn(seed, "commit", "-q", "--allow-empty", "-m", "release-only")
	e.gitIn(seed, "tag", "v1.0")
	e.gitIn(seed, "push", "-q", "origin", "v1.0")
	e.MustUp("web", up)
	e.gitIn(filepath.Join(e.ws(), "up"), "remote", "set-url", "origin", "https://git.example.invalid/up.git") // a remote off the server
	r := e.Down("web", false, "DELETE")
	r.HasNot(t, "UNSAVED WORK")
	if e.kept() {
		t.Error("not deleted")
	}
}

func TestStopIgnoredListDespiteGitConfig(t *testing.T) {
	e := boot(t)
	proj := e.pushedRepo()
	e.gitIn(proj, "config", "status.showUntrackedFiles", "no")
	e.write(filepath.Join(proj, "build", "out"), "x\n")
	e.Down("web", false, "DELETE").Has(t, "Ignored by git", "build/")
}
