//go:build !windows

package cli

import (
	"os"
	"os/exec"
	"syscall"
)

// prepare puts the process in a group of its own: a Ctrl-C at the terminal of viper ui goes to viper ui, which interrupts
// the process once (and in order); it must not reach the process a second time by the terminal, which makes a second Ctrl-C
// of it (signals.go: that ends it at once).
func prepare(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// interrupt is Ctrl-C for the process alone; its children (ssh) are ended by viper itself, in order.
func interrupt(cmd *exec.Cmd) error { return cmd.Process.Signal(os.Interrupt) }
