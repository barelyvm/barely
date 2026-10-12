package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"barely/internal/krun"
	"barely/internal/vmtypes"
)

// netFD and parentDeathFD are the fd numbers the host process hands "barely
// vm".
const (
	netFD         = 3
	parentDeathFD = 4
)

// orphanExitGrace covers the agent stopping dockerd (up to 10 s) and
// powering off.
const orphanExitGrace = 20 * time.Second

func init() {
	// krun_start_enter must run on the thread that configured the context.
	runtime.LockOSThread()
}

// runVM implements the "barely vm" subcommand: the VM process. It reads a
// VMSpec on stdin, validates it (libkrun aborts the whole process on a
// device failure, so everything checkable is checked first), configures
// libkrun and enters the guest.
func runVM() error {
	var spec vmtypes.VMSpec
	if err := json.NewDecoder(os.Stdin).Decode(&spec); err != nil {
		return fmt.Errorf("barely vm: reading VMSpec from stdin: %w", err)
	}
	if err := validateSpec(spec); err != nil {
		return fmt.Errorf("barely vm: %w", err)
	}

	vm, err := krun.New()
	if err != nil {
		return err
	}
	if err := vm.Configure(spec, netFD); err != nil {
		return err
	}
	shutdownFD, err := vm.ShutdownEventFD()
	if err != nil {
		return err
	}

	requestShutdown := func(reason string) {
		fmt.Fprintf(os.Stderr, "barely vm: requesting guest shutdown: %s\n", reason)
		var buf [8]byte
		buf[0] = 1
		if _, err := unix.Write(shutdownFD, buf[:]); err != nil {
			fmt.Fprintf(os.Stderr, "barely vm: writing shutdown eventfd: %v\n", err)
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	go func() {
		<-sigCh
		requestShutdown("SIGTERM")
	}()

	// fd 4 is the read end of a pipe only the host process's write end
	// holds; EOF means the host process died, so there's never an
	// orphaned VM.
	go func() {
		io.Copy(io.Discard, os.NewFile(uintptr(parentDeathFD), "parent-death"))
		requestShutdown("parent process exited")
		// Nobody is left to SIGKILL us if the guest ignores the request.
		time.Sleep(orphanExitGrace)
		fmt.Fprintf(os.Stderr, "barely vm: guest still running %s after parent exited, exiting\n", orphanExitGrace)
		os.Exit(1)
	}()

	// On success libkrun takes over this thread and calls exit() itself
	// when the guest powers off; this only returns on a config error.
	return vm.Enter()
}

func validateSpec(spec vmtypes.VMSpec) error {
	if spec.CPUs <= 0 {
		return fmt.Errorf("CPUs must be positive, got %d", spec.CPUs)
	}
	if spec.MemoryBytes == 0 {
		return fmt.Errorf("MemoryBytes must be set")
	}
	for name, path := range map[string]string{"Kernel": spec.Kernel, "RootImage": spec.RootImage, "DataImage": spec.DataImage} {
		if path == "" {
			return fmt.Errorf("%s must be set", name)
		}
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if spec.Initrd != "" {
		if _, err := os.Stat(spec.Initrd); err != nil {
			return fmt.Errorf("Initrd: %w", err)
		}
	}
	for _, s := range spec.Shares {
		if _, err := os.Stat(s.HostPath); err != nil {
			return fmt.Errorf("share %s: %w", s.Tag, err)
		}
	}
	for _, p := range spec.Vsock {
		if len(p.Path) > 103 {
			return fmt.Errorf("vsock port %d: socket path %q exceeds 104 bytes", p.Port, p.Path)
		}
	}
	return nil
}
