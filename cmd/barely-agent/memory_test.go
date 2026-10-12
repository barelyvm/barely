//go:build linux

package main

import (
	"testing"
	"time"
)

func TestSettled(t *testing.T) {
	now := time.Now()
	window := func(sizes ...uint64) []cacheSample {
		var h []cacheSample
		for i, b := range sizes {
			h = append(h, cacheSample{at: now.Add(-settleWindow + time.Duration(i)*reclaimInterval), bytes: b})
		}
		return h
	}
	cases := []struct {
		name string
		h    []cacheSample
		want bool
	}{
		{"empty", nil, false},
		{"too new", []cacheSample{{now.Add(-time.Second), 1 << 30}}, false},
		{"flat", window(2<<30, 2<<30, 2<<30, 2<<30, 2<<30, 2<<30, 2<<30), true},
		{"shrinking", window(3<<30, 2<<30, 2<<30, 1<<30, 1<<30, 1<<30, 1<<30), true},
		{"growing", window(1<<30, 1<<30, 2<<30, 2<<30, 3<<30, 3<<30, 3<<30), false},
		{"small growth", window(1<<30, 1<<30, 1<<30, 1<<30+settleGrowth/2, 1<<30, 1<<30, 1<<30), true},
	}
	for _, c := range cases {
		if got := settled(c.h, now); got != c.want {
			t.Errorf("%s: settled = %v, want %v", c.name, got, c.want)
		}
	}
}
