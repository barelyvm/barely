// Package lock is the host process's single-instance guard: a second instance that
// finds the lock already held exits 0, since another instance already owns
// the VM.
package lock

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// ErrHeld means another process already holds the lock.
var ErrHeld = fmt.Errorf("lock: already held by another process")

// Lock is a held flock on run/serve.lock.
type Lock struct {
	f *os.File
}

// Acquire takes a non-blocking exclusive flock on path, creating it if
// needed. It returns ErrHeld if another process already holds it.
func Acquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lock: opening %s: %w", path, err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if err == unix.EWOULDBLOCK {
			return nil, ErrHeld
		}
		return nil, fmt.Errorf("lock: flock %s: %w", path, err)
	}
	return &Lock{f: f}, nil
}

// Release drops the lock and closes the file.
func (l *Lock) Release() error {
	unix.Flock(int(l.f.Fd()), unix.LOCK_UN)
	return l.f.Close()
}
