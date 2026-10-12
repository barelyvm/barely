package main

import "io"

// blankProbe is how much of the data disk must read as zeroes for it to count
// as never formatted. A new data.img is a sparse file, so all of it reads as
// zeroes, and every filesystem writes something inside its first megabyte.
const blankProbe = 1 << 20

// isBlank reports whether the start of a disk is all zeroes. mountDataDisk
// formats only a disk that passes: a disk that holds anything and won't mount
// is damaged, and formatting it would destroy every image and volume.
func isBlank(disk io.Reader) (bool, error) {
	buf := make([]byte, blankProbe)
	n, err := io.ReadFull(disk, buf)
	// A disk shorter than the probe is judged on what it has.
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return false, err
	}
	for _, b := range buf[:n] {
		if b != 0 {
			return false, nil
		}
	}
	return true, nil
}
