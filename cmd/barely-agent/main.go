// Command barely-agent is the guest-side half of barely.
// It runs inside the Linux VM:
// when it's PID 1 it finishes booting the guest (mounts, network, dockerd);
// run any other way (a local test) it skips that. Either way it
// then serves the control channel on vsock 1024 and proxies vsock 2375 to
// dockerd's socket.
package main

import (
	"log"
	"os"
)

// version is the guest image version (GuestManifest.Version),
// set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	log.SetFlags(0)
	log.SetPrefix("barely-agent: ")

	a := newAgent(version)

	// The control channel and Docker proxy come up before bootstrap's
	// mounts, network and dockerd: hello only needs vsock, and starting
	// it first means a boot that hangs later (network, dockerd) still
	// answers hello and shows its own progress on the console, instead
	// of looking identical to a hung kernel.
	if os.Getpid() == 1 {
		if err := mountKernelFS(); err != nil {
			log.Fatalf("bootstrap: %v", err)
		}
		setHostname()
		go watchShutdownKey()
		go reclaimLoop()
		go func() {
			if err := bootstrap(); err != nil {
				log.Fatalf("bootstrap: %v", err)
			}
		}()
	}

	if err := a.run(); err != nil {
		log.Fatalf("%v", err)
	}
}
