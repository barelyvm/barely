//go:build linux

package main

import (
	"fmt"
	"os"
	"strings"

	"barely/internal/proto"
)

const (
	fseventDevice = "/dev/barely-fsevent"
	// fseventMaxWrite matches the kernel device's per-write limit.
	fseventMaxWrite = 60 << 10
)

// applyFsEvents writes host file changes to the kernel's fsevent device
// as "<op> <path>" lines, batching as many as fit in one write.
func applyFsEvents(events []proto.FsEvent) (int, error) {
	f, err := os.OpenFile(fseventDevice, os.O_WRONLY, 0)
	if err != nil {
		return 0, fmt.Errorf("fs_events: %w", err)
	}
	defer f.Close()
	var b strings.Builder
	applied := 0
	flush := func() error {
		if b.Len() == 0 {
			return nil
		}
		_, err := f.WriteString(b.String())
		b.Reset()
		return err
	}
	for _, ev := range events {
		if len(ev.Op) != 1 || !strings.HasPrefix(ev.Path, "/") || strings.ContainsAny(ev.Path, "\n\x00") {
			continue
		}
		line := ev.Op + " " + ev.Path + "\n"
		if b.Len()+len(line) > fseventMaxWrite {
			if err := flush(); err != nil {
				return applied, err
			}
		}
		b.WriteString(line)
		applied++
	}
	return applied, flush()
}
