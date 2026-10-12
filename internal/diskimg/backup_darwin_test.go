package diskimg

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// TestSetBackup pins that leaving a file out of backups writes the same
// attribute tmutil does, byte for byte, and that including it again removes
// it. Time Machine only honours the exact value, so the comparison is against
// what the installed tmutil writes; that half is skipped where tmutil doesn't
// exist.
func TestSetBackup(t *testing.T) {
	read := func(path string) []byte {
		buf := make([]byte, 256)
		n, err := unix.Getxattr(path, backupExcludeAttr, buf)
		if err != nil {
			return nil
		}
		return buf[:n]
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "data.img")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SetBackup(path, false); err != nil {
		t.Fatal(err)
	}
	ours := read(path)
	if ours == nil {
		t.Fatal("left out of backups, but the exclusion attribute is not set")
	}
	if _, err := exec.LookPath("tmutil"); err == nil {
		ref := filepath.Join(dir, "ref")
		if err := os.WriteFile(ref, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("tmutil", "addexclusion", ref).CombinedOutput(); err != nil {
			t.Fatalf("tmutil addexclusion: %v: %s", err, out)
		}
		if theirs := read(ref); !bytes.Equal(ours, theirs) {
			t.Errorf("attribute value differs from tmutil's:\n ours   %x\n tmutil %x", ours, theirs)
		}
	}

	if err := SetBackup(path, true); err != nil {
		t.Fatal(err)
	}
	if read(path) != nil {
		t.Fatal("included in backups again, but the exclusion attribute is still set")
	}
	// Including a file that was never left out is not an error.
	if err := SetBackup(path, true); err != nil {
		t.Fatal(err)
	}
}
