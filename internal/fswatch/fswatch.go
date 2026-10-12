//go:build darwin

// Package fswatch turns macOS FSEvents under the folders bind-mounted into
// running containers into the change records the guest's fsevent device
// applies.
package fswatch

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsevents"

	"barely/internal/proto"
)

// coalesce is how long changes are gathered before a batch goes out: long
// enough that a git checkout of hundreds of files is one batch, short
// enough that a save feels instant.
const coalesce = 20 * time.Millisecond

// Watcher watches a set of roots and calls send with coalesced batches.
type Watcher struct {
	send func([]proto.FsEvent)

	mu     sync.Mutex
	roots  []root
	stream *fsevents.EventStream
	stop   chan struct{}
}

// root maps a watched folder as the guest knows it (the bind-mount source
// dockerd was given, e.g. /tmp/app) to its real path, which is what
// FSEvents reports (/private/tmp/app).
type root struct {
	guest, real string
}

func New(send func([]proto.FsEvent)) *Watcher {
	return &Watcher{send: send}
}

// SetRoots replaces the watched folders. Nested folders collapse into
// their parent.
func (w *Watcher) SetRoots(paths []string) {
	roots := collapse(paths)
	w.mu.Lock()
	defer w.mu.Unlock()
	if equalRoots(roots, w.roots) {
		return
	}
	w.stopLocked()
	w.roots = roots
	if len(roots) == 0 {
		return
	}
	real := make([]string, len(roots))
	for i, r := range roots {
		real[i] = r.real
	}
	es := &fsevents.EventStream{
		Paths:   real,
		Latency: 10 * time.Millisecond,
		Flags:   fsevents.FileEvents | fsevents.NoDefer,
	}
	if err := es.Start(); err != nil {
		return
	}
	w.stream = es
	w.stop = make(chan struct{})
	go w.run(es, roots, w.stop)
}

// Close stops watching.
func (w *Watcher) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopLocked()
	w.roots = nil
}

func (w *Watcher) stopLocked() {
	if w.stream == nil {
		return
	}
	close(w.stop)
	w.stream.Stop()
	w.stream = nil
}

func (w *Watcher) run(es *fsevents.EventStream, roots []root, stop chan struct{}) {
	pending := map[string]string{}
	var flush <-chan time.Time
	for {
		select {
		case <-stop:
			return
		case batch, ok := <-es.Events:
			if !ok {
				return
			}
			for _, ev := range batch {
				p := ev.Path
				if !strings.HasPrefix(p, "/") {
					p = "/" + p
				}
				guestPath, ok := toGuest(roots, p)
				if !ok {
					continue
				}
				if op := classify(ev.Flags, p); op != "" {
					pending[guestPath] = merge(pending[guestPath], op)
				}
			}
			if flush == nil && len(pending) > 0 {
				flush = time.After(coalesce)
			}
		case <-flush:
			flush = nil
			w.send(drain(pending))
		}
	}
}

// classify maps one FSEvents record to C, M or D by checking the path
// now: FSEvents flags accumulate (a quickly created then edited file
// carries both), and a rename shows up once per side with the same flag.
// Anything created or renamed into place goes as C, which the guest
// resolves itself: a name it already has is re-looked-up, so an in-place
// edit becomes a modify and an atomic save (new file renamed over the old)
// a replacement.
func classify(flags fsevents.EventFlags, path string) string {
	// FSEvents lost events under this directory: report the directory as
	// changed so the guest refreshes it and watchers rescan it.
	if flags&(fsevents.MustScanSubDirs|fsevents.UserDropped|fsevents.KernelDropped) != 0 {
		return "M"
	}
	_, err := os.Lstat(path)
	exists := err == nil
	switch {
	case !exists:
		if flags&(fsevents.ItemRemoved|fsevents.ItemRenamed) != 0 {
			return "D"
		}
		return ""
	case flags&(fsevents.ItemCreated|fsevents.ItemRenamed) != 0:
		return "C"
	case flags&(fsevents.ItemModified|fsevents.ItemInodeMetaMod) != 0:
		return "M"
	}
	return ""
}

// merge combines two ops for the same path within one batch; the later
// state wins, except that a create followed by edits stays a create.
func merge(prev, next string) string {
	if prev == "C" && next == "M" {
		return "C"
	}
	return next
}

func drain(pending map[string]string) []proto.FsEvent {
	events := make([]proto.FsEvent, 0, len(pending))
	for p, op := range pending {
		events = append(events, proto.FsEvent{Op: op, Path: p})
		delete(pending, p)
	}
	// Parents before children, so a new directory exists before its files.
	sort.Slice(events, func(i, j int) bool { return events[i].Path < events[j].Path })
	return events
}

func toGuest(roots []root, p string) (string, bool) {
	for _, r := range roots {
		if p == r.real || strings.HasPrefix(p, r.real+"/") {
			return r.guest + strings.TrimPrefix(p, r.real), true
		}
	}
	return "", false
}

func collapse(paths []string) []root {
	clean := make([]string, 0, len(paths))
	for _, p := range paths {
		clean = append(clean, filepath.Clean(p))
	}
	sort.Strings(clean)
	var out []root
	for _, p := range clean {
		if len(out) > 0 {
			last := out[len(out)-1].guest
			if p == last || strings.HasPrefix(p, last+"/") {
				continue
			}
		}
		out = append(out, root{guest: p, real: realPath(p)})
	}
	return out
}

// realPath resolves symlinks in the longest prefix of p that exists, so a
// folder that doesn't exist yet still maps /tmp to /private/tmp.
func realPath(p string) string {
	rest := ""
	for cur := p; cur != "/" && cur != "."; cur = filepath.Dir(cur) {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		rest = filepath.Join(filepath.Base(cur), rest)
	}
	return p
}

func equalRoots(a, b []root) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
