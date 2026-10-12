package footprint

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

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
