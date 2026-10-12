//go:build linux

package main

import (
	"fmt"
	"log"
	"os"

	"golang.org/x/sys/unix"
)

const (
	binfmtDir = "/proc/sys/fs/binfmt_misc"
	stubPath  = "/usr/libexec/barely/amd64-unavailable"

	// x86-64 ELF executables and shared objects (e_machine 0x3e).
	x86Magic = `\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\x3e\x00`
	x86Mask  = `\xff\xff\xff\xff\xff\xfe\xfe\x00\xff\xff\xff\xff\xff\xff\xff\xff\xfe\xff\xff\xff`
)

// registerAmd64Stub makes x86-64 binaries print the unavailable message
// instead of failing with "exec format error".
func registerAmd64Stub() {
	if err := unix.Mount("binfmt_misc", binfmtDir, "binfmt_misc", 0, ""); err != nil && err != unix.EBUSY {
		log.Printf("amd64: mount binfmt_misc: %v", err)
		return
	}
	// F: the stub is opened now, so containers use it without mounting anything.
	rule := fmt.Sprintf(":amd64-unavailable:M::%s:%s:%s:F", x86Magic, x86Mask, stubPath)
	if err := os.WriteFile(binfmtDir+"/register", []byte(rule), 0); err != nil {
		log.Printf("amd64: register with binfmt_misc: %v", err)
	}
}
