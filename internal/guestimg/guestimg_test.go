package guestimg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeImage(t *testing.T, guestDir, version string, protocol int) string {
	t.Helper()
	dir := filepath.Join(guestDir, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := `{"version":"` + version + `","agent_protocol":` + string(rune('0'+protocol)) + `}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(m), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestFindSkipsOlderProtocol pins that a newer-looking image with the wrong
// protocol does not shadow an older usable one.
func TestFindSkipsOlderProtocol(t *testing.T) {
	guestDir := t.TempDir()
	writeImage(t, guestDir, "2026.01.01.0000", 2)
	writeImage(t, guestDir, "2026.02.01.0000", 1)

	img, err := Find(guestDir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if img.Manifest.Version != "2026.01.01.0000" {
		t.Fatalf("got %s, want the protocol 2 image", img.Manifest.Version)
	}
}

// TestFindOnlyOldGuestsSaysMakeGuest pins the error a user sees after
// upgrading barely without rebuilding the guest.
func TestFindOnlyOldGuestsSaysMakeGuest(t *testing.T) {
	guestDir := t.TempDir()
	writeImage(t, guestDir, "2026.01.01.0000", 1)

	_, err := Find(guestDir, 2)
	if err == nil || !strings.Contains(err.Error(), "too old") || !strings.Contains(err.Error(), "make guest") {
		t.Fatalf("got %v, want a too-old error naming make guest", err)
	}
}

// TestOpenRejectsOldGuest pins that Open refuses an older protocol as too old
// and accepts the wanted one.
func TestOpenRejectsOldGuest(t *testing.T) {
	dir := writeImage(t, t.TempDir(), "2026.01.01.0000", 1)

	_, err := Open(dir, 2)
	if err == nil || !strings.Contains(err.Error(), "too old") {
		t.Fatalf("got %v, want a too-old error", err)
	}
	if _, err := Open(dir, 1); err != nil {
		t.Fatal(err)
	}
}

// TestVerify pins what setup relies on to refuse a damaged guest image: files
// that match the manifest's checksums pass, and one that has changed since
// fails with an error naming it.
func TestVerify(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Image")
	if err := os.WriteFile(path, []byte("kernel"), 0o644); err != nil {
		t.Fatal(err)
	}
	img := Image{Dir: dir}
	// sha256 of "kernel".
	img.Manifest.SHA256 = map[string]string{"Image": "6923dd1bc0460082c5d55a831908c24a282860b7f1cd6c2b79cf1bc8857c639c"}
	if err := img.Verify(); err != nil {
		t.Fatalf("intact image: %v", err)
	}

	if err := os.WriteFile(path, []byte("kernal"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := img.Verify(); err == nil || !strings.Contains(err.Error(), "Image") {
		t.Fatalf("got %v, want a checksum error naming Image", err)
	}
}
