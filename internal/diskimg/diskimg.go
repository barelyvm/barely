// Package diskimg manages the sparse data disk (data.img): libkrun truncates
// the file when a discard reaches its end, so it must be re-extended to
// the configured cap before every boot.
package diskimg

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// EnsureSize makes sure path exists and is at least capBytes, sparsely. If
// the file is already that size or larger (DiskMax never shrinks below the
// current logical size), it's left alone.
func EnsureSize(path string, capBytes uint64) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("diskimg: %s: %w", path, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("diskimg: %s: %w", path, err)
	}
	if uint64(info.Size()) >= capBytes {
		return nil
	}
	if err := f.Truncate(int64(capBytes)); err != nil {
		return fmt.Errorf("diskimg: extending %s to %d bytes: %w", path, capBytes, err)
	}
	return nil
}

// Usage reports path's logical size (its apparent length) and its on-disk
// size (blocks actually allocated), for "status"'s disk logical-vs-on-disk
// display.
func Usage(path string) (logical, onDisk uint64, err error) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return 0, 0, fmt.Errorf("diskimg: %s: %w", path, err)
	}
	// st_blocks is always in 512-byte units regardless of the
	// filesystem's actual block size (stat(2)).
	return uint64(st.Size), uint64(st.Blocks) * 512, nil
}
