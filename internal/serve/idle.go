package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"time"
)

// idleCheckInterval is how often the idle loop re-evaluates; it doesn't
// need to be as fine-grained as idle_timeout itself.
const idleCheckInterval = 5 * time.Second

// idleLoop implements idle shutdown: once there are no open
// clients, no open shell sessions and no running containers for
// Config.IdleTimeout, it shuts the guest down and cancels cancel, which
// Run treats exactly like an external SIGTERM (clean exit).
func (s *Server) idleLoop(ctx context.Context, cancel context.CancelFunc) {
	if s.cfg.IdleTimeout <= 0 {
		return // 0 disables idle shutdown
	}
	ticker := time.NewTicker(idleCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			idle, since := s.checkIdle()
			if !idle {
				continue
			}
			if elapsed := time.Since(since); elapsed >= time.Duration(s.cfg.IdleTimeout) {
				log.Printf("serve: idle for %s, shutting down", elapsed.Round(time.Second))
				// VM down first, then end Run: the process exits only once there is
				// no guest left. launchd starts a new one on the next docker.sock
				// connection.
				s.shutdown()
				cancel()
				return
			}
		}
	}
}

// checkIdle reports whether the server is currently idle (no clients, no
// shell sessions, no running containers) and since when; it resets the
// idle clock the moment any of those become non-zero.
func (s *Server) checkIdle() (idle bool, since time.Time) {
	busy := s.clients.Load() > 0 || s.shellSessions.Load() > 0
	if !busy {
		// Queried before taking mu: it is a round trip to dockerd.
		n, err := s.runningContainers()
		if err != nil {
			// Can't tell: don't shut down on a guess.
			busy = true
		} else {
			busy = n > 0
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if busy {
		s.idleSince = time.Time{}
		return false, time.Time{}
	}
	if s.idleSince.IsZero() {
		s.idleSince = time.Now()
	}
	return true, s.idleSince
}

// idleIn reports how long until the server would shut down if it stayed
// idle from now, for "status"'s idle_in. Zero if idle shutdown is
// disabled or the server isn't currently idle.
func (s *Server) idleIn() time.Duration {
	if s.cfg.IdleTimeout <= 0 {
		return 0
	}
	s.mu.Lock()
	since := s.idleSince
	s.mu.Unlock()
	if since.IsZero() {
		return 0
	}
	remaining := time.Duration(s.cfg.IdleTimeout) - time.Since(since)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// runningContainers asks dockerd directly over the vsock passthrough path
// (the same one every docker.sock client uses) for the number of running
// containers. It returns (0, nil) when there's no VM up yet to ask —
// trivially zero, not "unknown".
func (s *Server) runningContainers() (int, error) {
	s.mu.Lock()
	ctl := s.ctl
	s.mu.Unlock()
	if ctl == nil || !ctl.Ready() {
		return 0, nil
	}

	conn, err := net.DialTimeout("unix", s.paths.V2375(), 2*time.Second)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))

	req, err := http.NewRequest(http.MethodGet, "http://unix/containers/json?all=0", nil)
	if err != nil {
		return 0, err
	}
	if err := req.Write(conn); err != nil {
		return 0, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var list []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return 0, err
	}
	return len(list), nil
}
