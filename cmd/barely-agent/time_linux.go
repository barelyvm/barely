//go:build linux

package main

import (
	"time"

	"golang.org/x/sys/unix"
)

// setClock implements set_time: resyncing the guest clock after Mac sleep.
func setClock(unixNS int64) error {
	t := time.Unix(0, unixNS)
	ts := unix.NsecToTimespec(t.UnixNano())
	return unix.ClockSettime(unix.CLOCK_REALTIME, &ts)
}
