//go:build !windows

package system_test

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/system"
)

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -2
}

func TestRunAttachedPassesTheExitCodeAndForwardsSignals(t *testing.T) {
	err := system.RunAttached(nil, io.Discard, io.Discard, "sh", "-c", "exit 3")
	if exitCode(err) != 3 {
		t.Errorf("exit code %d (%v)", exitCode(err), err)
	}
	// SIGTERM sent to viper is passed on to the child
	go func() {
		time.Sleep(300 * time.Millisecond)
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			t.Error(err)
		}
	}()
	start := time.Now()
	err = system.RunAttached(nil, io.Discard, io.Discard, "sleep", "30")
	if err == nil || time.Since(start) > 10*time.Second {
		t.Fatalf("the child was not ended: %v after %v", err, time.Since(start))
	}
	if exitCode(err) != -1 {
		t.Errorf("a child ended by a signal reports %d", exitCode(err))
	}
	// SIGINT (Ctrl-C) reaches the child with the terminal, not through us; viper only survives it
	go func() {
		time.Sleep(300 * time.Millisecond)
		if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
			t.Error(err)
		}
	}()
	if err := system.RunAttached(nil, io.Discard, io.Discard, "sleep", "1"); err != nil {
		t.Errorf("viper survives SIGINT and waits: %v", err)
	}
}

func TestIsTerminalIsOnlyTrueForARealTerminal(t *testing.T) {
	if system.IsTerminal("not a file") || system.IsTerminal(nil) {
		t.Error("only files can be terminals")
	}
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = null.Close() }()
	if system.IsTerminal(null) {
		t.Error("/dev/null is no terminal")
	}
}
