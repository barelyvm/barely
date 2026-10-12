// The suite protects Read against the live proc_pid_rusage call (macOS only).
// Run with go test ./internal/footprint.
package footprint

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestReadSelf pins that Read returns a non-zero footprint for a live pid.
func TestReadSelf(t *testing.T) {
	b, err := Read(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if b == 0 {
		t.Fatal("expected non-zero footprint")
	}
	t.Logf("footprint: %d bytes", b)
}

// TestReadOtherProcess pins that Read works on another process's pid, which
// is how the VM process is measured from the host process.
func TestReadOtherProcess(t *testing.T) {
	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	time.Sleep(200 * time.Millisecond)
	b, err := Read(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("other process footprint: %d bytes", b)
}
