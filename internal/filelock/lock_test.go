package filelock_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/filelock"
)

func TestTryLockSaysBusyAtOnceAndGivesTheLockBackWhenReleased(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	unlock, err := filelock.TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := filelock.TryLock(path); !errors.Is(err, filelock.ErrBusy) {
		t.Fatalf("a second lock on a held file: %v", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("TryLock waited %v", took)
	}
	unlock()
	again, err := filelock.TryLock(path)
	if err != nil {
		t.Fatalf("after the release: %v", err)
	}
	again()
}
