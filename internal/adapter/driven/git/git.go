// Package git asks the git program on this computer whether a repository can be read.
package git

import (
	"context"
	"os"
	"os/exec"
)

// Available reports whether git is installed.
func Available() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// LsRemote reports whether the repository can be read without credentials (git ls-remote).
func LsRemote(ctx context.Context, url string) error {
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--", url)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd.Run()
}
