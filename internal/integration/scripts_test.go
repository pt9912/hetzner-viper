package integration

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// The scripts that run on the server (and the fake programs of the tests) must at least parse.
func TestScriptsThatRunOnTheServerParse(t *testing.T) {
	tool(t, "bash")
	var files []string
	for _, pattern := range []string{"../start/scripts/*.sh", "../check/probe.sh", "../stop/remote.sh", "../servertest/remote/*"} {
		m, _ := filepath.Glob(pattern)
		files = append(files, m...)
	}
	if len(files) < 15 {
		t.Fatalf("only %d scripts found: %v", len(files), files)
	}
	for _, f := range files {
		if out, err := exec.Command("bash", "-n", f).CombinedOutput(); err != nil {
			t.Errorf("%s does not parse: %v\n%s", f, err, out)
		}
	}
}

// With shellcheck at hand, the scripts for the server also pass it (errors only, as before).
func TestScriptsThatRunOnTheServerPassShellcheck(t *testing.T) {
	tool(t, "shellcheck")
	var files []string
	for _, pattern := range []string{"../start/scripts/*.sh", "../check/probe.sh", "../stop/remote.sh"} {
		m, _ := filepath.Glob(pattern)
		files = append(files, m...)
	}
	args := append([]string{"-S", "error", "-s", "bash"}, files...)
	if out, err := exec.Command("shellcheck", args...).CombinedOutput(); err != nil {
		t.Errorf("shellcheck: %v\n%s", err, out)
	}
}
