package lock

import (
	"path/filepath"
	"testing"
)

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
