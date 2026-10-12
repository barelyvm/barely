//go:build linux

package main

import (
	"encoding/binary"
	"testing"
)

func inputEvent(typ, code uint16, value int32) []byte {
	b := make([]byte, inputEventSize)
	binary.LittleEndian.PutUint16(b[16:], typ)
	binary.LittleEndian.PutUint16(b[18:], code)
	binary.LittleEndian.PutUint32(b[20:], uint32(value))
	return b
}

// TestIsShutdownKey pins that only the press of libkrun's restart key is a
// shutdown request, so key release and sync events don't power the guest off.
// Linux-only, like the code under test.
func TestIsShutdownKey(t *testing.T) {
	cases := []struct {
		name string
		ev   []byte
		want bool
	}{
		{name: "restart key press", ev: inputEvent(evKey, keyRestart, 1), want: true},
		{name: "restart key release", ev: inputEvent(evKey, keyRestart, 0), want: false},
		{name: "other key", ev: inputEvent(evKey, 116, 1), want: false},
		{name: "sync event", ev: inputEvent(0, 0, 0), want: false},
	}
	for _, c := range cases {
		if got := isShutdownKey(c.ev); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
