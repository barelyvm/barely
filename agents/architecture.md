# Architecture

How the three programs work together. Symbols are named so you can grep for them; the detail lives in their doc comments.

## A docker command, end to end

1. launchd owns `~/.barely/docker.sock`. Nothing of barely runs until something connects.
2. The first connection makes launchd start `barely serve`, the host process. `Server.Run` takes the listener from launchd, or binds the path itself when run by hand.
3. Each connection gets a goroutine (`handleClient`) that calls `ensureVM`. If no VM is up, that boots one; connections that arrive during the boot wait for it.
4. `dockerproxy.Splice` copies raw bytes between the client and a unix socket that the VM process bridges to vsock port 2375. There is no HTTP parsing, so attach, exec and build streams work unchanged.
5. In the guest, the agent's docker proxy splices that vsock port onto dockerd's socket.

A client that cannot be served gets an Engine-API error body, so the docker CLI prints barely's message.

## Boot

`ensureVM` is the only way a VM starts, and `bootMu` makes concurrent callers share one boot. In order:

1. Find the `barelyvm` helper and check it carries the hypervisor entitlement.
2. Pick the newest guest image whose agent protocol matches (`guestimg.Find`).
3. Extend `data.img` to the configured cap (`diskimg.EnsureSize`).
4. Build the `VMSpec`: CPUs, memory cap, kernel, initrd, root image, data disk, shares, vsock ports, and a kernel command line carrying the guest arguments.
5. Create the network socket pair (`netstack`) and start `barelyvm run` with the spec on stdin (`vmproc.Start`).
6. `barelyvm` configures libkrun and enters the guest. libkrun then owns that process and exits it when the guest powers off.
7. The host dials the agent's control port and exchanges `hello`. From here the per-boot loops start: published ports, trim scheduling, file events and the crash watcher.

In the guest, the agent starts its vsock servers before anything else, so a boot that hangs later still answers `hello`. It then mounts a read-only root under a tmpfs overlay, mounts the data disk at `/var/lib/docker`, mounts the shares at the same paths they have on the Mac, registers the amd64 stub, brings up the network, and starts dockerd.

## Shutdown

Every path ends in `Server.shutdown`: a last fstrim, a `shutdown` request to the agent, then stopping the VM process. The agent stops dockerd, acknowledges, unmounts the data disk and powers off.

- **`barely stop`**: answers only once the VM is down, then the host process exits. From the moment it is asked, the process boots nothing more; a client that arrives in that window is told to retry.
- **Idle**: with no clients and no running containers for the idle timeout, the host shuts the VM down and exits. launchd starts it again on the next connection.
- **`barely restart`**: shutdown, reload the config, boot again in the same process.
- **VM crash**: the host process stays up and the next client boots a new VM. Three crashes in a minute stop the retries.

The host holds the write end of a pipe the VM process reads. If the host dies, the VM process sees it close and shuts the guest down, so a VM is never orphaned.

## Elastic memory

Three pieces, in three places:

- **The guest kernel reports freed pages** (free page reporting, enabled on the kernel command line).
- **The libkrun fork releases them to macOS**, and periodically drops host page-table entries that would otherwise be counted twice.
- **The agent's reclaim loop** drops page cache once it has stopped growing, down to a small floor, because Linux would otherwise keep cache forever and the VM would sit at its cap after a build.

`internal/footprint` reads the VM process's footprint, the number macOS charges it, for `barely status` and `barely top`.

## Elastic disk

`data.img` is one sparse file at the configured cap, holding everything under `/var/lib/docker`. Space returns to macOS through fstrim, which the host asks the agent to run ten minutes after it sees a deletion in dockerd's events and once more before shutdown. There is no `discard` mount option. The libkrun fork frees the blocks without ever changing the file's length.

## Files and networking

- **Shares** are virtio-fs mounts of Mac directories. The virtio-fs server never tells the guest when a file changes on the Mac, so `internal/fswatch` watches the shared roots that containers have mounted and sends the changes to the agent, which writes them to the kernel's fsevent device. That is what makes file watchers in containers fire.
- **Network** is gvisor-tap-vsock running inside the host process, connected to the guest's NIC by a socket pair. The subnet and the guest's address are fixed.
- **Published ports**: dockerd maps the port inside the guest; `internal/portforward` follows dockerd's events and exposes each one on the Mac.

## State on disk

Everything lives under `~/.barely` (`internal/state`): `bin/` (installed binaries), `guest/<version>/` (guest images), `data.img`, `config.toml`, `logs/`, `run/` (the lock and the host ends of the vsock ports), and the two sockets, `docker.sock` and `control.sock`.

Config keys are `memory_max`, `cpus`, `disk_max`, `idle_timeout`, `shares` and `start_at_login`. The host process reads the file at start and on restart; the VM process and the guest never read it.
