package filelock

import (
	"errors"
	"os"
	"runtime"

	"golang.org/x/sys/windows"
)

// Lock takes an exclusive lock on a file until the returned function is called.
func Lock(path string) (func(), error) {
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

// ErrBusy is returned by TryLock when another process (or this one, through another file handle) holds the lock.
var ErrBusy = errors.New("the file is locked by another process")

// TryLock is Lock that does not wait: it takes the lock or says that somebody else has it (ErrBusy). The lock is released when
// the returned function is called or the process ends.
func TryLock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	h := windows.Handle(f.Fd())
	ol := new(windows.Overlapped)
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol); err != nil {
		_ = f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return func() { _ = windows.UnlockFileEx(h, 0, 1, 0, ol); _ = f.Close(); runtime.KeepAlive(f) }, nil
}
