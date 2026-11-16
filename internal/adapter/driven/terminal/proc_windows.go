//go:build windows

package terminal

import "syscall"

// detached puts the window in a process group of its own (CREATE_NEW_PROCESS_GROUP).
func detached() *syscall.SysProcAttr { return &syscall.SysProcAttr{CreationFlags: 0x00000200} }
