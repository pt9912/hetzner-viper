package main

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// runChild runs a program on our terminal. Ctrl-C reaches it with the terminal; viper itself must
// survive it and wait, so that it can clean up. SIGTERM and SIGHUP are forwarded: they do not
// reach the child on their own.
func runChild(name string, args ...string) error {
	cmd := exec.CommandContext(context.Background(), name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
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

// isTerminal reports whether a file is a terminal (a character device other than /dev/null).
func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	if err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(st, null) {
		return false
	}
	return true
}
