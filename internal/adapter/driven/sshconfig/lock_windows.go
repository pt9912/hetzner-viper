package sshconfig

import (
	"os"
	"runtime"

	"golang.org/x/sys/windows"
)

// lockFile takes an exclusive lock on a file until the returned function is called.
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	h := windows.Handle(f.Fd())
	ol := new(windows.Overlapped)
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, ol); err != nil {
		_ = f.Close() // the lock failed; the close error adds nothing
		return nil, err
	}
	return func() { _ = windows.UnlockFileEx(h, 0, 1, 0, ol); _ = f.Close(); runtime.KeepAlive(f) }, nil
}
