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
	// fseventMaxWrite stays under the kernel device's 64 KiB per-write limit.
	fseventMaxWrite = 60 << 10
)

// applyFsEvents implements the "fs_events" control message. libkrun's
// virtio-fs server never notifies the guest of host-side changes, so the host
// reports them and this hands them to the kernel's fsevent device
// (kernel/patches), which invalidates the guest's caches and raises inotify
// events. Lines are "<op> <path>"; batching keeps a burst to few writes.
// Returns how many events were written, skipping malformed ones.
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
		// The device parses one record per line, so a newline or NUL in a path
		// would forge or truncate a record; relative paths can't be resolved.
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
