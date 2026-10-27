//go:build !windows

package cli

import (
	"os"
	"os/exec"
	"syscall"
)

// replaceProcess runs a program in place of viper: it gets the terminal and all signals, and its
// exit code is ours.
func replaceProcess(name string, args ...string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return err
	}
	return syscall.Exec(path, append([]string{name}, args...), os.Environ())
}
