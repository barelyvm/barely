# Agent notes

barely is a Docker host VM for macOS (Apple Silicon) whose memory and disk grow and shrink with use. `docker` on the Mac talks to dockerd in a Linux VM; when nothing is running the VM shuts down, and memory and disk the guest frees go back to macOS.

## Three programs

- **`barely`** (`cmd/barely`): the CLI, and, run by launchd as `barely serve`, the **host process** (`internal/serve`). It owns the Docker socket, boots the VM on the first connection and shuts it down when idle.
- **`barelyvm`** (`cmd/barelyvm`): the **VM process**. A separate helper, the only binary that links libkrun (`internal/krun`, cgo) and the only one signed with the hypervisor entitlement. The host process starts one per boot. There is no `barely vm` subcommand.
- **`barely-agent`** (`cmd/barely-agent`): the guest's init (PID 1). It boots the guest, starts dockerd, and answers the host process over vsock.

## Stack

- **Go** for all three programs. No web framework: the control API is `net/http` over a unix socket.
- **libkrun** (barely's fork) is the hypervisor library, on Apple's Hypervisor framework. The fork returns guest memory to macOS and never shrinks the disk image.
- **gvisor-tap-vsock** (barely's fork) is the guest's network, run in-process (`internal/netstack`). The fork makes guest DNS follow network and VPN changes.
- **Linux kernel** with two patches (`kernel/patches`): a device that delivers host file-change events to the guest, and a virtio-fs mount parameter.
- **Alpine** guest image with dockerd (`guest/`), built in Docker.
- **launchd** socket activation starts the host process on demand.

## Layout

- `cmd/barely`, `cmd/barelyvm`, `cmd/barely-agent`: the three programs.
- `internal/serve`: the host process: boot, shutdown, idle, the control API.
- `internal/proto`, `internal/control`: the host–guest control protocol and its host-side client.
- `internal/vmtypes`, `internal/vmproc`, `internal/krun`: the VM spec, starting the VM process, the libkrun wrapper.
- `internal/guestimg`, `internal/guestargs`: choosing a guest image; arguments passed on the kernel command line.
- `internal/netstack`, `internal/portforward`, `internal/dockerproxy`: guest network, published ports, the Docker socket splice.
- `internal/fswatch`, `internal/footprint`, `internal/dockerdesktop`: host file events, memory measurement, Docker Desktop detection.
- `internal/config`, `internal/state`, `internal/diskimg`, `internal/launchd`, `internal/lock`: config file, paths under `~/.barely`, the data disk, the launchd agent, the single-instance lock.
- `kernel/`, `guest/`: kernel build and guest image recipe. `scripts/`: live test scripts.

## Commands

```sh
make libkrun            # fetch and build the libkrun fork pinned in libkrun.env
make barely barelyvm    # host binaries into out/bin (barelyvm is ad-hoc signed)
make guest              # kernel + agent + guest image, installed to ~/.barely/guest
go build ./... && go vet ./... && go test ./...
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o /dev/null ./cmd/barely-agent
./out/bin/barely setup  # install into ~/.barely/bin and (re)load the launchd agent
```

## Gotchas

- **Rebuilding does not change the running install.** launchd runs the copies in `~/.barely/bin`; re-run `barely setup` to update them. A new agent needs `make guest` as well.
- **Test in a throwaway instance, not the real one.** Copy `out/bin/*` to `/tmp/<name>/bin`, run `make guest BARELY_HOME=/tmp/<name>`, then `BARELY_HOME=/tmp/<name> /tmp/<name>/bin/barely serve -state-dir /tmp/<name>` and point `DOCKER_HOST` at its `docker.sock`. Keep the path short: unix socket paths are limited to 104 bytes.
- **`make kernel` and `make guest` need a running Docker**, which is normally barely itself.
- **The agent is a Linux program.** Host builds compile stubs, so always cross-build and cross-vet it before calling a change done.
- **Bump the protocol in two places.** `Version` in `internal/proto` and `AGENT_PROTOCOL` in the Makefile must match, or the host refuses the guest image.
- **amd64 images are not supported.** The guest registers a stub that prints a clear message. Rosetta is not used, as Apple is phasing it out.
- **Never create a `doc.go`.** Package docs go on the `package` clause of the primary file. Comments say why, not what.
- **Forks:** a fork change is a commit in the fork, a push, then a commit of the new pin here. A pin must be a commit the fork's remote has.

## More depth

- [agents/architecture.md](agents/architecture.md): how a docker command reaches the guest, boot and shutdown, and how memory, disk and file events work.
- [agents/protocol.md](agents/protocol.md): the vsock ports, the control messages, and what must stay in step between host and guest.
- [agents/forks.md](agents/forks.md): working on the libkrun and gvisor-tap-vsock forks and pinning them.
