//go:build darwin

package fswatch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"barely/internal/proto"
)

func TestCollapseAndToGuest(t *testing.T) {
	roots := collapse([]string{"/tmp/a/b", "/tmp/a", "/Users/x/proj"})
	if len(roots) != 2 {
		t.Fatalf("collapse kept %d roots, want 2: %+v", len(roots), roots)
	}
	got, ok := toGuest(roots, "/private/tmp/a/b/c.go")
	if !ok || got != "/tmp/a/b/c.go" {
		t.Errorf("toGuest(/private/tmp/a/b/c.go) = %q, %v; want /tmp/a/b/c.go", got, ok)
	}
	if _, ok := toGuest(roots, "/private/tmp/ab"); ok {
		t.Error("toGuest matched a sibling with a shared prefix")
	}
}

// TestWatcher drives real FSEvents: a create, an edit and a delete under a
// watched temp dir must each reach send, with the guest-side path.
func TestWatcher(t *testing.T) {
	dir := t.TempDir()
	got := make(chan []proto.FsEvent, 16)
	w := New(func(b []proto.FsEvent) { got <- b })
	w.SetRoots([]string{dir})
	defer w.Close()
	time.Sleep(200 * time.Millisecond)

	f := filepath.Join(dir, "main.go")
	expect := func(op string, act func()) {
		t.Helper()
		act()
		deadline := time.After(3 * time.Second)
		for {
			select {
			case batch := <-got:
				for _, ev := range batch {
					if ev.Path == f && ev.Op == op {
						return
					}
				}
			case <-deadline:
				t.Fatalf("no %s event for %s", op, f)
			}
		}
	}
	expect("C", func() { os.WriteFile(f, []byte("package main\n"), 0o644) })
	time.Sleep(100 * time.Millisecond)
	expect("C", func() { os.WriteFile(f, []byte("package main\n// edit\n"), 0o644) })
	time.Sleep(100 * time.Millisecond)
	expect("D", func() { os.Remove(f) })
}
