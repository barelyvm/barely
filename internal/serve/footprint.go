package serve

import (
	"time"

	"barely/internal/dockerdesktop"
	"barely/internal/footprint"
)

// footprintSampleInterval matches "barely top", a live terminal view
// refreshed every second. footprint.Read is a direct proc_pid_rusage
// call (cheap, no subprocess), so sampling it every second even while idle
// costs nothing worth throttling for.
const footprintSampleInterval = time.Second

// sampleFootprint records the VM process's memory footprint on the Mac
// into s.footHist until vm exits, so "status" and "top" can show the
// current value next to its recent peak.
func (s *Server) sampleFootprint(vm interface{ Pid() int }, done <-chan struct{}) {
	ticker := time.NewTicker(footprintSampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case now := <-ticker.C:
			b, err := footprint.Read(vm.Pid())
			if err != nil {
				continue
			}
			s.footHist.Add(footprint.Sample{At: now, Bytes: b})
		}
	}
}

// dockerDesktopFootprint returns Docker Desktop's VM footprint and true
// when it's running, or (0, false) when it isn't.
func dockerDesktopFootprint() (uint64, bool) {
	return dockerdesktop.Footprint()
}
