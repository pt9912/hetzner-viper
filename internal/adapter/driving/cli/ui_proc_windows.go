//go:build windows

package cli

import (
	"os/exec"
	"syscall"
)

const ctrlBreakEvent = 1

// prepare gives the process a group of its own, so that the break below reaches it and not the terminal of viper ui.
func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// interrupt sends CTRL_BREAK to the group of the process (Go reads it like Ctrl-C). Not verified on a real Windows yet.
func interrupt(cmd *exec.Cmd) error {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GenerateConsoleCtrlEvent")
	if r, _, err := proc.Call(ctrlBreakEvent, uintptr(cmd.Process.Pid)); r == 0 {
		return err
	}
	return nil
}
