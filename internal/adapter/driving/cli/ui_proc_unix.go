//go:build !windows

package cli

import (
	"os"
	"os/exec"
)

func prepare(*exec.Cmd) {}

// interrupt is Ctrl-C for the process alone; its children (ssh) are ended by viper itself, in order.
func interrupt(cmd *exec.Cmd) error { return cmd.Process.Signal(os.Interrupt) }
