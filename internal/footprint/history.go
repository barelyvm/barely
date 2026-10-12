package footprint

import (
	"sync"
	"time"
)

// Sample is one footprint reading at a point in time.
type Sample struct {
	At    time.Time
	Bytes uint64
}

// History keeps a rolling window of footprint samples plus the all-time peak
// since the VM booted. Capacity bounds memory use: the newest sample
// overwrites the oldest once full.
type History struct {
	mu   sync.Mutex
	buf  []Sample
	next int
	full bool
	peak Sample
}

// NewHistory returns a History holding up to capacity samples (e.g. one
// hour at 1 s).
func NewHistory(capacity int) *History {
	if capacity < 1 {
		capacity = 1
	}
	return &History{buf: make([]Sample, capacity)}
}

// Add records a new sample, updating the peak if it's the highest seen.
func (h *History) Add(s Sample) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.buf[h.next] = s
	h.next = (h.next + 1) % len(h.buf)
	if h.next == 0 {
		h.full = true
	}
	if s.Bytes >= h.peak.Bytes {
		h.peak = s
	}
}

// Reset clears the history and peak, for a fresh boot of the VM process.
func (h *History) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next = 0
	h.full = false
	h.peak = Sample{}
}

// Peak returns the highest sample recorded since the last Reset.
func (h *History) Peak() Sample {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.peak
}

// Latest returns the most recent sample, or the zero Sample if none yet.
func (h *History) Latest() Sample {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.full && h.next == 0 {
		return Sample{}
	}
	i := h.next - 1
	if i < 0 {
		i = len(h.buf) - 1
	}
	return h.buf[i]
}

// Samples returns all recorded samples, oldest first.
func (h *History) Samples() []Sample {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.full {
		out := make([]Sample, h.next)
		copy(out, h.buf[:h.next])
		return out
	}
	out := make([]Sample, len(h.buf))
	n := copy(out, h.buf[h.next:])
	copy(out[n:], h.buf[:h.next])
	return out
}
