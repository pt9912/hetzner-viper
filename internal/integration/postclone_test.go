package integration_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// postCloneSettings run a command that records where it ran and fails until the file "go" exists
// in the server's state directory.
const postCloneSettings = `VIPER_POST_CLONE='echo "ran in $PWD" >> "$MOCK_STATE/post"
test -e "$MOCK_STATE/go"'
`

func ran(e *Env) int { return strings.Count(e.Srv.File("post"), "ran in ") }

func TestPostCloneRunsOnceInTheClonedRepositoryAfterHomebrew(t *testing.T) {
	tool(t, "git")
	e := New(t, "")
	e.Machine("web", "VIPER_BREW_PACKAGES=ripgrep\nVIPER_POST_CLONE='echo \"ran in $PWD\" >> \"$MOCK_STATE/post\"'\n")
	origin := originRepo(t)
	r := e.MustUp("web", origin)
	work := filepath.Join(e.Config("web").WorkspaceDir, "origin")
	if got := e.Srv.File("post"); got != "ran in "+work+"\n" {
		t.Errorf("post-clone ran: %q, want it in %s", got, work)
	}
	if b, p := lineNo(r.Out, "brew: installed"), lineNo(r.Out, "post-clone: done"); b < 0 || b >= p {
		t.Errorf("Homebrew (line %d) must come before the post-clone commands (line %d):\n%s", b, p, r.Out)
	}
	if !strings.Contains(e.Events(), "post-clone commands done") {
		t.Errorf("not logged:\n%s", e.Events())
	}
	e.MustUp("web", origin)
	if n := ran(e); n != 1 {
		t.Errorf("ran %d times, want once (not again for an existing clone)", n)
	}
}

func TestPostCloneDoesNotRunForARepositoryThatWasClonedBefore(t *testing.T) {
	tool(t, "git")
	e := New(t, "")
	origin := originRepo(t)
	e.Machine("web", "")
	e.MustUp("web", origin)
	e.Machine("web", postCloneSettings)
	e.Srv.Write("go", "")
	e.MustUp("web", origin)
	if n := ran(e); n != 0 {
		t.Errorf("ran %d times for a clone without a pending marker", n)
	}
}

func TestPostCloneFailureIsRetriedByDefaultAndLogged(t *testing.T) {
	tool(t, "git")
	e := New(t, "")
	e.Machine("web", postCloneSettings)
	origin := originRepo(t)
	r := e.Up("web", origin)
	if r.Code != 0 {
		t.Fatalf("code %d, a failure must only warn:\n%s", r.Code, r.Out)
	}
	r.Has(t, "post-clone: FAILED", "WARNING: the VIPER_POST_CLONE commands failed", "rerun viper up web to try again", "Ready: ssh hetzner-claude-web")
	if !strings.Contains(e.Events(), "post-clone commands FAILED (on error: retry)") {
		t.Errorf("failure not logged:\n%s", e.Events())
	}
	e.MustUp("web", origin)
	if n := ran(e); n != 2 {
		t.Errorf("ran %d times, want 2 (retried)", n)
	}
	e.Srv.Write("go", "")
	e.MustUp("web", origin).Has(t, "post-clone: done")
	e.MustUp("web", origin)
	if n := ran(e); n != 3 {
		t.Errorf("ran %d times, want 3 (success ends the retries)", n)
	}
}

func TestPostCloneOnErrorIgnoreTriesOnlyOnce(t *testing.T) {
	tool(t, "git")
	e := New(t, "")
	e.Machine("web", postCloneSettings+"VIPER_POST_CLONE_ON_ERROR=ignore\n")
	origin := originRepo(t)
	r := e.Up("web", origin)
	if r.Code != 0 {
		t.Fatalf("code %d:\n%s", r.Code, r.Out)
	}
	r.Has(t, "post-clone: FAILED", "not tried again")
	if !strings.Contains(e.Events(), "post-clone commands FAILED (on error: ignore)") {
		t.Errorf("failure not logged:\n%s", e.Events())
	}
	e.MustUp("web", origin)
	if n := ran(e); n != 1 {
		t.Errorf("ran %d times, want once", n)
	}
}

func TestPostCloneOnErrorAbortFailsTheStartAndKeepsTheServer(t *testing.T) {
	tool(t, "git")
	e := New(t, "")
	e.Machine("web", postCloneSettings+"VIPER_POST_CLONE_ON_ERROR=abort\n")
	origin := originRepo(t)
	r := e.Up("web", origin)
	if r.Code != 1 {
		t.Fatalf("code %d, want 1:\n%s", r.Code, r.Out)
	}
	r.Has(t, "the VIPER_POST_CLONE commands failed", "Server retained")
	r.HasNot(t, "Ready:")
	if !strings.Contains(e.Events(), "post-clone commands FAILED (on error: abort)") {
		t.Errorf("failure not logged:\n%s", e.Events())
	}
	if e.Cloud.Servers[e.Config("web").ServerName] == nil {
		t.Error("the server was deleted")
	}
	e.Srv.Write("go", "")
	e.MustUp("web", origin).Has(t, "post-clone: done")
}

func TestPostCloneNeedsARepositoryAndAValidOnErrorValue(t *testing.T) {
	e := New(t, "")
	e.Machine("web", postCloneSettings+"VIPER_POST_CLONE_ON_ERROR=skip\n")
	r := e.Preflight("web", "")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "VIPER_POST_CLONE_ON_ERROR must be retry, ignore or abort: skip", "VIPER_POST_CLONE without a repository: nothing to run")
}
