//go:build !linux

package main

import "barely/internal/proto"

// mountKernelFS, bootstrap, watchShutdownKey, stopDockerd and powerOff are
// guest-only (bootstrap.go, powerkey.go); this binary only ever runs as PID 1
// under Linux, but these stubs let the package build and vet on the host platform too.

func setHostname() {}

func mountKernelFS() error { return nil }

func bootstrap() error { return nil }

func watchShutdownKey() {}

func reclaimLoop() {}

func applyFsEvents(events []proto.FsEvent) (int, error) { return 0, nil }

func sendMemStats(*controlConn) {}

func stopDockerd() {}

func powerOff() {}

func setClock(unixNS int64) error { return nil }

func fstrimDataDisk() (uint64, error) { return 0, nil }
