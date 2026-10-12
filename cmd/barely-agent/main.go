// Command barely-agent is the guest-side half of barely: the init (PID 1) of
// the Linux VM that barelyvm boots, and the guest end of every vsock port the
// host process uses. guest/build.sh installs it as /init in the initrd.
//
// As PID 1 it boots the guest (kernel filesystems, read-only root with a
// tmpfs overlay, data disk, shares, network, dockerd) and powers it off on
// the host's request; run any other way (a local test) it skips that. Either
// way it serves three vsock ports to the host process (internal/serve):
// 1024 control (internal/proto messages: hello, shutdown, set_time,
// fs_events, fstrim, shell_open, shell_resize; unsolicited ready, mem_stats
// and shell_exit), 1025 raw "barely shell" session streams, and 2375 a splice
// to dockerd's socket.
package main

import (
	"log"
	"os"
)

// version is the guest image version (GuestManifest.Version),
// set at build time with -ldflags "-X main.version=...".
var version = "dev"

// bootFailedPrefix starts the console line that says why the guest could not
// boot. internal/serve looks for it (with the logger's "barely-agent: " in
// front) to tell the docker client the reason.
const bootFailedPrefix = "bootstrap: "

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
				// Power off rather than exit: PID 1 exiting panics the kernel
				// and leaves the VM hung, while a powered-off guest ends the
				// VM process at once and the host reports this line
				// (bootFailedPrefix is how it finds it).
				log.Printf(bootFailedPrefix+"%v", err)
				powerOff()
			}
		}()
	}

	if err := a.run(); err != nil {
		log.Fatalf("%v", err)
	}
}
