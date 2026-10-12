//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"

	"golang.org/x/sys/unix"
)

// fstrimReport parses fstrim -v's "<mountpoint>: N bytes were trimmed" line.
var fstrimReport = regexp.MustCompile(`(\d+) bytes? (?:were |was )?trimmed`)

// fstrimDataDisk implements the "fstrim" control message:
// trims the data disk mounted at dataDir and
// reports the bytes reclaimed.
func fstrimDataDisk() (uint64, error) {
	// ext4 only releases deleted blocks once its journal commits (every
	// 5 s), so a trim right after a delete would skip them. syncfs forces
	// the commit first.
	if f, err := os.Open(dataDir); err == nil {
		unix.Syncfs(int(f.Fd()))
		f.Close()
	}
	out, err := exec.Command("fstrim", "-v", dataDir).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("fstrim: %w: %s", err, out)
	}
	m := fstrimReport.FindSubmatch(out)
	if m == nil {
		return 0, fmt.Errorf("fstrim: unexpected output: %s", out)
	}
	n, err := strconv.ParseUint(string(m[1]), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("fstrim: %w", err)
	}
	return n, nil
}
