package serve

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"barely/internal/control"
	"barely/internal/fswatch"
	"barely/internal/proto"
)

// fsEventsChunk caps one fs_events message; the guest device takes 64 KiB
// per write, and a git checkout can touch thousands of files.
const fsEventsChunk = 500

// fsEventsLoop forwards Mac file changes under the folders bind-mounted
// into running containers to the guest for one VM's lifetime. Only those folders are watched: nothing else can
// have a watcher in a container.
func (s *Server) fsEventsLoop(ctx context.Context, ctl *control.Client, shares []string) {
	w := fswatch.New(func(events []proto.FsEvent) {
		for len(events) > 0 {
			n := min(len(events), fsEventsChunk)
			if err := ctl.Request(proto.TypeFsEvents, proto.FsEvents{Events: events[:n]}, nil, 10*time.Second); err != nil {
				log.Printf("serve: fs_events: %v", err)
				return
			}
			events = events[n:]
		}
	})
	defer w.Close()

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", s.paths.V2375())
		},
	}}
	refresh := func() {
		roots, err := bindSources(ctx, client, shares)
		if err != nil {
			return
		}
		w.SetRoots(roots)
	}

	filters := url.QueryEscape(`{"type":["container"],"event":["start","die","destroy"]}`)
	for ctx.Err() == nil {
		refresh()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/events?filters="+filters, nil)
		if err != nil {
			return
		}
		resp, err := client.Do(req)
		if err == nil {
			dec := json.NewDecoder(resp.Body)
			for {
				var ev struct{}
				if err := dec.Decode(&ev); err != nil {
					break
				}
				refresh()
			}
			resp.Body.Close()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

// bindSources lists the bind-mount sources of running containers that sit
// under a shared root.
func bindSources(ctx context.Context, client *http.Client, shares []string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/containers/json", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var containers []struct {
		Mounts []struct {
			Type   string `json:"Type"`
			Source string `json:"Source"`
		} `json:"Mounts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return nil, err
	}
	var out []string
	for _, c := range containers {
		for _, m := range c.Mounts {
			if m.Type == "bind" && underShare(m.Source, shares) {
				out = append(out, m.Source)
			}
		}
	}
	return out, nil
}

func underShare(p string, shares []string) bool {
	p = filepath.Clean(p)
	for _, sh := range shares {
		sh = filepath.Clean(sh)
		if p == sh || strings.HasPrefix(p, sh+"/") {
			return true
		}
	}
	return false
}
