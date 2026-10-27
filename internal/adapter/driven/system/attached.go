package system

import (
	"context"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

// RunAttached runs a program on the terminal given by the streams. Ctrl-C reaches it with the terminal; viper itself must
// survive it and wait, so that it can clean up. SIGTERM and SIGHUP are forwarded: they do not
// reach the child on their own.
func RunAttached(stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(context.Background(), name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sig)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sig:
				if s != syscall.SIGINT {
					_ = cmd.Process.Signal(s)
				}
			case <-done:
				return
			}
		}
	}()
	err := cmd.Wait()
	close(done)
	return err
}

// IsTerminal reports whether a stream is a terminal: a file that is a character device other than
// /dev/null. Anything that is not a file (a buffer, a pipe) is not.
func IsTerminal(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(st, null) {
		return false
	}
	return true
}

// Output runs a program and returns what it wrote to its standard output.
func Output(name string, args ...string) (string, error) {
	out, err := exec.CommandContext(context.Background(), name, args...).Output()
	return string(out), err
}

// LookPath is the path of a program in the PATH.
func LookPath(file string) (string, error) { return exec.LookPath(file) }

// HostEnvironment is the home directory and the environment of this process.
func HostEnvironment() (settings.Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return settings.Env{}, err
	}
	return settings.Env{Lookup: os.LookupEnv, Home: home}, nil
}
