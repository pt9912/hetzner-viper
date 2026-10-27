// Package system runs programs on this computer: the adapter of the port internal/port/driven/system.
package system

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"strings"
	"time"
)

// Run runs a program with the given input and returns what it printed.
func Run(ctx context.Context, stdin, name string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 5 * time.Second // a program that left children holding the pipes cannot hang us
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	return out.String(), errb.String(), err
}

// Stream runs a program with its output going to the user as it comes.
func Stream(ctx context.Context, stdin string, stdout, stderr io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 5 * time.Second
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	return cmd.Run()
}

// KeygenError is a failed ssh-keygen with what it printed.
type KeygenError struct {
	Err error
	Out string
}

func (e *KeygenError) Error() string {
	if e.Out != "" {
		return e.Err.Error() + ": " + e.Out
	}
	return e.Err.Error()
}

// Keygen runs ssh-keygen (or another program) and fails with what it printed.
func Keygen(name string, args ...string) error {
	out, err := exec.CommandContext(context.Background(), name, args...).CombinedOutput()
	if err != nil {
		return &KeygenError{Err: err, Out: strings.TrimSpace(string(out))}
	}
	return nil
}
