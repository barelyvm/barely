package serve

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"barely/internal/control"
)

// Lazy disk shrinking: no continuous discard
// and no periodic timer. One fstrim runs trimDelay after the last
// deletion dockerd reports, and one before every clean shutdown.
const (
	trimDelay   = 10 * time.Minute
	trimTimeout = 2 * time.Minute
)

// deletion reports whether a dockerd event frees space on the data disk.
func deletion(typ, action string) bool {
	switch typ {
	case "image":
		return action == "delete" || action == "prune"
	case "container":
		return action == "destroy" || action == "prune"
	case "volume":
		return action == "destroy" || action == "prune"
	case "builder":
		return action == "prune"
	}
	return false
}

// trimLoop watches dockerd's events for one VM's lifetime and schedules
// the lazy fstrim.
func (s *Server) trimLoop(ctx context.Context, ctl *control.Client) {
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", s.paths.V2375())
		},
	}}
	var mu sync.Mutex
	var timer *time.Timer
	defer func() {
		mu.Lock()
		if timer != nil {
			timer.Stop()
		}
		mu.Unlock()
	}()
	schedule := func() {
		mu.Lock()
		defer mu.Unlock()
		// A new deletion restarts the countdown, so a burst of deletions
		// (prune, compose down) trims once, after it settles.
		pending := timer != nil
		if pending {
			timer.Stop()
		}
		if !pending {
			log.Printf("serve: deletion seen, fstrim scheduled in %s", trimDelay)
		}
		timer = time.AfterFunc(trimDelay, func() {
			// The VM may have exited while the timer ran; its ctl is dead then.
			if ctx.Err() == nil {
				s.fstrim(ctl, "after deletions")
			}
			mu.Lock()
			timer = nil
			mu.Unlock()
		})
	}

	filters := url.QueryEscape(`{"type":["image","container","volume","builder"]}`)
	for ctx.Err() == nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/events?filters="+filters, nil)
		if err != nil {
			return
		}
		resp, err := client.Do(req)
		if err == nil {
			dec := json.NewDecoder(resp.Body)
			for {
				var ev struct {
					Type   string `json:"Type"`
					Action string `json:"Action"`
				}
				if err := dec.Decode(&ev); err != nil {
					break
				}
				if deletion(ev.Type, ev.Action) {
					schedule()
				}
			}
			resp.Body.Close()
		}
		// The stream fails while dockerd is still starting and ends when it
		// restarts; retry until the VM's ctx ends.
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

// fstrim asks the guest to trim the data disk so deleted Docker data stops
// occupying space on the Mac. Failures are only logged: a missed trim costs
// disk space, not correctness.
func (s *Server) fstrim(ctl *control.Client, why string) {
	n, err := ctl.Fstrim(trimTimeout)
	if err != nil {
		log.Printf("serve: fstrim (%s): %v", why, err)
		return
	}
	log.Printf("serve: fstrim (%s): trimmed %d MiB", why, n>>20)
}
