package lock

import (
	"path/filepath"
	"testing"
)

// TestAcquireRelease pins the single-instance guarantee: while one Acquire is
// held a second on the same path gets ErrHeld (each Acquire opens its own file
// description, so flock conflicts even within one process), and Release makes
// the path acquirable again. It does not cover release on process death.
func TestAcquireRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "serve.lock")

	l1, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(path); err != ErrHeld {
		t.Fatalf("second Acquire: got %v, want ErrHeld", err)
	}
	if err := l1.Release(); err != nil {
		t.Fatal(err)
	}
	l2, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	l2.Release()
}
