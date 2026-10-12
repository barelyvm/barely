// Package krun wraps the libkrun C API (barely's fork) used
// by the VM process (barelyvm) to boot the guest.
package krun

/*
#cgo CFLAGS: -I${SRCDIR}/../../out/include
#cgo LDFLAGS: -L${SRCDIR}/../../out/bin -lkrun.1
#include <stdlib.h>
#include <stdbool.h>
#include "libkrun.h"
*/
import "C"

import (
	"fmt"
	"net"
	"path/filepath"
	"unsafe"

	"barely/internal/vmtypes"
)

// VM is one configuration context, from krun_create_ctx to krun_start_enter.
type VM struct {
	ctx C.uint32_t
}

func check(what string, rc C.int32_t) error {
	if rc < 0 {
		return fmt.Errorf("krun_%s: %d", what, int(rc))
	}
	return nil
}

func cstr(s string) *C.char { return C.CString(s) }

// New creates a configuration context. SetLogLevel is called once per
// process; callers only need one VM per process.
func New() (*VM, error) {
	if err := check("set_log_level", C.krun_set_log_level(2)); err != nil {
		return nil, err
	}
	rc := C.krun_create_ctx()
	if err := check("create_ctx", rc); err != nil {
		return nil, err
	}
	return &VM{ctx: C.uint32_t(rc)}, nil
}

// Configure applies spec to the context: CPUs/memory, kernel, disks,
// shares, vsock ports and console. netFD is the VM process's end
// of the socketpair handed to gvisor-tap-vsock (fd 3); pass -1
// to skip networking.
func (vm *VM) Configure(spec vmtypes.VMSpec, netFD int) error {
	if err := check("set_vm_config", C.krun_set_vm_config(vm.ctx, C.uint8_t(spec.CPUs), C.uint32_t(spec.MemoryBytes/(1<<20)))); err != nil {
		return err
	}
	var initrd *C.char
	if spec.Initrd != "" {
		initrd = cstr(spec.Initrd)
	}
	if err := check("set_kernel", C.krun_set_kernel(vm.ctx, cstr(spec.Kernel), 0, initrd, cstr(spec.Cmdline))); err != nil {
		return err
	}
	if err := check("disable_implicit_init", C.krun_disable_implicit_init(vm.ctx)); err != nil {
		return err
	}
	if spec.ConsoleLog != "" {
		if err := check("set_console_output", C.krun_set_console_output(vm.ctx, cstr(spec.ConsoleLog))); err != nil {
			return err
		}
	}
	if err := check("disable_implicit_vsock", C.krun_disable_implicit_vsock(vm.ctx)); err != nil {
		return err
	}
	if err := check("add_vsock", C.krun_add_vsock(vm.ctx, 0)); err != nil {
		return err
	}
	for _, p := range spec.Vsock {
		if err := check(fmt.Sprintf("add_vsock_port2(%d)", p.Port), C.krun_add_vsock_port2(vm.ctx, C.uint32_t(p.Port), cstr(p.Path), C.bool(p.Listen))); err != nil {
			return err
		}
	}
	// vda: root, read-only. vdb: data, read-write.
	if err := check("add_disk2(root)", C.krun_add_disk2(vm.ctx, cstr("root"), cstr(spec.RootImage), C.KRUN_DISK_FORMAT_RAW, C.bool(true))); err != nil {
		return err
	}
	if err := check("add_disk2(data)", C.krun_add_disk2(vm.ctx, cstr("data"), cstr(spec.DataImage), C.KRUN_DISK_FORMAT_RAW, C.bool(false))); err != nil {
		return err
	}
	for _, s := range spec.Shares {
		// libkrun opens the share root with O_NOFOLLOW, and /tmp and
		// /var/folders are symlinks into /private on macOS.
		dir, err := filepath.EvalSymlinks(s.HostPath)
		if err != nil {
			return fmt.Errorf("krun: share %s: %w", s.HostPath, err)
		}
		if err := check("add_virtiofs("+s.Tag+")", C.krun_add_virtiofs(vm.ctx, cstr(s.Tag), cstr(dir))); err != nil {
			return err
		}
	}
	if netFD >= 0 {
		mac, err := parseMAC(vmtypes.GuestMAC)
		if err != nil {
			return err
		}
		if err := check("add_net_unixgram", C.krun_add_net_unixgram(vm.ctx, nil, C.int(netFD), (*C.uint8_t)(unsafe.Pointer(&mac[0])), 0, C.NET_FLAG_VFKIT)); err != nil {
			return err
		}
	}
	return nil
}

// ShutdownEventFD enables the guest shutdown device and returns an eventfd
// the caller owns; writing to it asks the guest to shut down orderly
// (krun_get_shutdown_eventfd / PL061 GPIO key). Must be called before Enter.
func (vm *VM) ShutdownEventFD() (int, error) {
	rc := C.krun_get_shutdown_eventfd(vm.ctx)
	if err := check("get_shutdown_eventfd", rc); err != nil {
		return 0, err
	}
	return int(rc), nil
}

// Enter calls krun_start_enter on the calling thread, which must be locked
// to an OS thread (runtime.LockOSThread). It only returns on a
// configuration error: on success libkrun takes over the process and calls
// exit() itself when the guest powers off, and a device that fails to
// activate aborts the process.
func (vm *VM) Enter() error {
	return check("start_enter", C.krun_start_enter(vm.ctx))
}

func parseMAC(s string) ([6]C.uint8_t, error) {
	hw, err := net.ParseMAC(s)
	if err != nil {
		return [6]C.uint8_t{}, fmt.Errorf("krun: bad MAC %q: %w", s, err)
	}
	var mac [6]C.uint8_t
	for i, b := range hw {
		mac[i] = C.uint8_t(b)
	}
	return mac, nil
}
