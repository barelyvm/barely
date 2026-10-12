//go:build linux

package main

import (
	"encoding/binary"
	"log"
	"os"
	"path/filepath"
)

// libkrun's shutdown eventfd (krun_get_shutdown_eventfd, which barelyvm
// fires on SIGTERM or when the host process dies) presses KEY_RESTART on
// its gpio-keys device. libkrun's own init handles that; ours has to.
const (
	evKey      = 1
	keyRestart = 0x198
	// struct input_event on arm64: 16-byte timeval, u16 type, u16 code,
	// s32 value.
	inputEventSize = 24
)

func isShutdownKey(ev []byte) bool {
	typ := binary.LittleEndian.Uint16(ev[16:])
	code := binary.LittleEndian.Uint16(ev[18:])
	value := int32(binary.LittleEndian.Uint32(ev[20:]))
	return typ == evKey && code == keyRestart && value == 1
}

// watchShutdownKey shuts the guest down the same way the control channel's
// "shutdown" does when the host asks through libkrun's shutdown eventfd.
func watchShutdownKey() {
	paths, err := filepath.Glob("/dev/input/event*")
	if err != nil || len(paths) == 0 {
		log.Printf("shutdown key: no input devices (%v); host shutdown requests will be ignored", err)
		return
	}
	pressed := make(chan struct{}, 1)
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			log.Printf("shutdown key: %v", err)
			continue
		}
		go func() {
			ev := make([]byte, inputEventSize)
			for {
				if _, err := f.Read(ev); err != nil {
					return
				}
				if isShutdownKey(ev) {
					pressed <- struct{}{}
					return
				}
			}
		}()
	}
	<-pressed
	log.Print("shutdown requested by host")
	stopDockerd()
	powerOff()
}
