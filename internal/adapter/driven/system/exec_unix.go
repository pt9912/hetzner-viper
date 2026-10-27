//go:build !windows

package system

import (
	"io"
	"os"
	"os/exec"
	"syscall"
)

// Exec runs a program in place of viper: it gets the terminal (the streams are the ones it is
// started with) and all signals, and its exit code is ours.
func Exec(_ io.Reader, _, _ io.Writer, name string, args ...string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return err
	}
	return syscall.Exec(path, append([]string{name}, args...), os.Environ())
}
