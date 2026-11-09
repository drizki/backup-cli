// Package lock provides the per-job run lock: an flock(2) on a file in the
// state directory. The kernel drops the lock when the process dies, so a
// crashed run never leaves a stale lock behind. Lock files are never deleted.
package lock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// ErrHeld means another process holds the lock.
var ErrHeld = errors.New("lock is held by another run")

// Lock is an acquired lock.
type Lock struct{ f *os.File }

// Acquire takes the exclusive lock without blocking and records the PID in
// the file for humans and doctor.
func Acquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrHeld
		}
		return nil, err
	}
	if err := f.Truncate(0); err == nil {
		_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	}
	return &Lock{f: f}, nil
}

// Close releases the lock.
func (l *Lock) Close() error { return l.f.Close() }

// Held reports whether the lock file exists and is currently locked. It
// never creates the file.
func Held(path string) (bool, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return true, nil
	}
	return false, err
}
