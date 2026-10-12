// Command barelyvm is the only part of barely that uses Apple's hypervisor,
// so it's the only binary that needs signing with the hypervisor entitlement.
// barely serve launches it (internal/vmproc) and it's never run by hand. "run"
// is the VM process: it reads a VMSpec on stdin, boots the guest through
// libkrun (internal/krun, cgo) and exits when the guest powers off.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: barelyvm run")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = runVM()
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "barelyvm:", err)
		os.Exit(1)
	}
}
