//go:build !linux

package main

import "barely/internal/proto"

// The functions stubbed here are guest-only (the linux-tagged files); the
// agent only ships for Linux, but these stubs let the package build, vet and
// run its platform-neutral tests on the host platform too.

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
