//go:build !windows

package filelock

import (
	"errors"
	"os"
	"syscall"
)

// Lock takes an exclusive lock on a file until the returned function is called.
func Lock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close() // the lock failed; the close error adds nothing
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}

// ErrBusy is returned by TryLock when another process (or this one, through another file handle) holds the lock.
var ErrBusy = errors.New("the file is locked by another process")

// TryLock is Lock that does not wait: it takes the lock or says that somebody else has it (ErrBusy). The lock is released when
// the returned function is called or the process ends (also when it is killed: nothing stays locked).
func TryLock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}
