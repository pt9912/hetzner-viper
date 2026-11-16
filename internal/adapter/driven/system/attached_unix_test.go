//go:build !windows

package system_test

import (
	"bytes"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/system"
)

// viper ui interrupts viper alone: a program that viper runs for viper ui must get the interrupt as well
func TestAnInterruptReachesTheProgramWhenViperRunsForTheUI(t *testing.T) {
	t.Setenv(system.UIChildEnv, "1")
	done := make(chan error, 1)
	go func() {
		done <- system.RunAttached(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{}, "sleep", "30")
	}()
	time.Sleep(500 * time.Millisecond)
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Error("sleep ended without the interrupt")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the program did not get the interrupt")
	}
}
