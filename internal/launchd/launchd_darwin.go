// Package launchd is the host process's end of socket activation:
// it reads
// the listening socket launchd already created for the "docker" entry in
// the LaunchAgent's Sockets dictionary, instead of binding the path itself.
package launchd

/*
#include <launch.h>
#include <errno.h>
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"net"
	"os"
	"unsafe"
)

// ActivateSocket retrieves the listener launchd set up for the Sockets
// entry named name. ok is false when this process wasn't started by
// launchd with that entry (ENOENT: no such socket; ESRCH: not managed by
// launchd at all) — the normal case for a plain "barely serve" run during
// development — and the caller should fall back to binding the path
// itself.
func ActivateSocket(name string) (ln net.Listener, ok bool, err error) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	var fds *C.int
	var cnt C.size_t
	rc := C.launch_activate_socket(cname, &fds, &cnt)
	if rc != 0 {
		if rc == C.ENOENT || rc == C.ESRCH {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("launchd: launch_activate_socket(%q): errno %d", name, rc)
	}
	defer C.free(unsafe.Pointer(fds))
	if cnt == 0 {
		return nil, false, fmt.Errorf("launchd: launch_activate_socket(%q): no descriptors", name)
	}

	// One socket entry can yield several fds (one per matching network
	// interface for TCP); a Unix-domain Sockets entry like ours always
	// yields exactly one, so only the first is used.
	fdSlice := unsafe.Slice(fds, int(cnt))
	f := os.NewFile(uintptr(fdSlice[0]), name)
	ln, err = net.FileListener(f)
	if err != nil {
		return nil, false, fmt.Errorf("launchd: wrapping fd for %q: %w", name, err)
	}
	return ln, true, nil
}
