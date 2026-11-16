//go:build !windows

package terminal

import "syscall"

// detached puts the window in a session of its own: it outlives viper, and an interrupt of viper does not reach it.
func detached() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
