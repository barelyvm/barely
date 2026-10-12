package main

import (
	"bytes"
	"testing"
)

// TestIsBlank pins what mountDataDisk is allowed to format: only a disk whose
// start is all zeroes. One stray byte anywhere in the probe, such as what is
// left of a filesystem whose superblock was lost, must keep the disk from
// being formatted.
func TestIsBlank(t *testing.T) {
	zero := make([]byte, blankProbe+4096)
	last := make([]byte, blankProbe)
	last[blankProbe-1] = 1
	// ext4 keeps its superblock magic at byte 1080.
	ext4 := make([]byte, blankProbe)
	ext4[1080], ext4[1081] = 0x53, 0xEF
	beyond := make([]byte, blankProbe+4096)
	beyond[blankProbe+10] = 1

	for _, tc := range []struct {
		name string
		disk []byte
		want bool
	}{
		{"all zeroes", zero, true},
		{"shorter than the probe", make([]byte, 512), true},
		{"empty", nil, true},
		{"ext4 superblock", ext4, false},
		{"data in the probe's last byte", last, false},
		{"data only past the probe", beyond, true},
	} {
		got, err := isBlank(bytes.NewReader(tc.disk))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: isBlank = %v, want %v", tc.name, got, tc.want)
		}
	}
}
