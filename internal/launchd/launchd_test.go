package launchd

import "testing"

// Outside launchd (as this test runs), ActivateSocket must report ok=false
// and no error rather than failing — the host process falls back to
// binding the path itself.
func TestActivateSocketOutsideLaunchd(t *testing.T) {
	ln, ok, err := ActivateSocket("docker")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		ln.Close()
		t.Fatal("expected ok=false when not running under launchd")
	}
}
