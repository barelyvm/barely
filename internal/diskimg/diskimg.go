// Package diskimg manages the sparse data disk (data.img): it is created at
// the configured cap and extended to it before every boot, so raising
// disk_max takes effect on the next start. The file is never shrunk here.
//
// Entry points are EnsureSize, Usage and SetBackup.
//
// Used by: internal/serve (EnsureSize and SetBackup before each boot, Usage
// for status) and cmd/barely setup (Usage, to refuse lowering an existing
// disk; SetBackup).
package diskimg

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// EnsureSize makes sure path exists and is at least capBytes, sparsely. If
// the file is already that size or larger it's left alone, so lowering
// disk_max by hand has no effect on an existing disk.
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
	// Truncate extends sparsely: no blocks are allocated until the guest
	// writes, so a 100 GiB cap costs nothing on the host up front.
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
