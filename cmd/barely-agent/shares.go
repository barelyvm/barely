//go:build linux

package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"

	"barely/internal/guestargs"
	"barely/internal/vmtypes"
)

// bootArgs reads what the host process passed on the kernel command line
// (shares to mount, Docker socket paths to alias); there is no other channel
// before the control connection is up.
func bootArgs() (guestargs.Args, error) {
	b, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return guestargs.Args{}, err
	}
	return guestargs.Parse(string(b)), nil
}

// mountShares mounts each shared root at the same path as on the Mac, so a
// bind mount source like /Users/me/app means the same directory on both
// sides.
func mountShares(shares []vmtypes.Share) error {
	for _, s := range shares {
		if err := os.MkdirAll(s.HostPath, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", s.HostPath, err)
		}
		if err := unix.Mount(s.Tag, s.HostPath, "virtiofs", 0, ""); err != nil {
			return fmt.Errorf("mount %s at %s: %w", s.Tag, s.HostPath, err)
		}
	}
	return nil
}
