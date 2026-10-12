package serve

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// wakeCheckInterval is how often wakeLoop samples the wall clock. macOS
// suspends every thread during sleep, including a time.Ticker's: when the
// wall-clock gap between two fires is much bigger than this interval, the
// Mac must have been asleep in between, with no separate notification
// needed. The comparison strips Go's monotonic reading, which on macOS
// doesn't advance during sleep and would hide the gap.
const wakeCheckInterval = 10 * time.Second

// wakeLogMin is the shortest gap worth a log line. A sleeping Mac wakes
// briefly about once a minute, and each of those is a gap too: the guest
// clock is still resynced for every one, but only a real wake is logged.
const wakeLogMin = 5 * time.Minute

// wakeLoop pushes the host time into the guest after the Mac wakes, because
// the guest clock falls behind while the host sleeps. SIGUSR1 triggers the
// same resync by hand.
func (s *Server) wakeLoop(ctx context.Context) {
	ticker := time.NewTicker(wakeCheckInterval)
	defer ticker.Stop()
	usr1 := make(chan os.Signal, 1)
	signal.Notify(usr1, syscall.SIGUSR1)
	defer signal.Stop(usr1)
	last := time.Now().Round(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-usr1:
			log.Print("serve: SIGUSR1, resyncing the guest clock")
			s.onWake()
		case <-ticker.C:
			// Round(0) strips the monotonic reading; see wakeCheckInterval.
			now := time.Now().Round(0)
			if gap := now.Sub(last); gap > wakeCheckInterval*2 {
				if gap >= wakeLogMin {
					log.Printf("serve: detected a gap of %s (Mac wake), resyncing the guest clock", gap.Round(time.Second))
				}
				s.onWake()
			}
			last = now
		}
	}
}

// onWake resyncs the guest clock; no-op when no VM is up.
func (s *Server) onWake() {
	s.mu.Lock()
	ctl := s.ctl
	s.mu.Unlock()
	if ctl == nil {
		return
	}
	if err := ctl.SetTime(time.Now(), 5*time.Second); err != nil {
		log.Printf("serve: set_time on wake: %v", err)
	}
}
