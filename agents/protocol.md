# Host–guest protocol

What crosses the boundary between the host process and the guest agent, and what has to change together.

## Vsock ports

The agent listens on three vsock ports. The VM process maps each to a unix socket under `~/.barely/run`, which is what the host process dials.

| Port | Carries |
|---|---|
| 1024 | The control channel: framed JSON messages, below. |
| 1025 | `barely shell` session streams, raw bytes after a short session header. |
| 2375 | A splice to dockerd's socket. |

The port numbers, the framing and every message type are defined in `internal/proto`. `internal/control` is the host's client; the agent's side is its control dispatch.

## Control messages

Each frame is a length prefix and a JSON message with a type, an id, an optional error and a payload. A request carries a non-zero id and the reply echoes it. Unsolicited messages use id zero. An unknown type gets an error reply.

| Message | Direction | Kind | Purpose |
|---|---|---|---|
| `hello` | host → guest | request | Exchange protocol versions. |
| `ready` | guest → host | unsolicited | dockerd is answering. |
| `mem_stats` | guest → host | unsolicited | The guest's memory view, every second. |
| `shutdown` | host → guest | request | Stop dockerd, acknowledge, unmount the data disk, power off. |
| `set_time` | host → guest | request | Set the guest clock after the Mac wakes. |
| `fstrim` | host → guest | request | Trim the data disk; the reply says how much. |
| `fs_events` | host → guest | request | A batch of file changes on the Mac to apply in the guest. |
| `shell_open` | host → guest | request | Start a shell session process. |
| `shell_resize` | host → guest | request | Resize a session's terminal. |
| `shell_exit` | guest → host | unsolicited | A session's process exited, with its code. |

## Before the control channel exists

The only way to tell the guest anything before `hello` is the kernel command line. `internal/guestargs` encodes the shares to mount, the Docker socket paths to alias and the data disk's path on the Mac; the agent parses them from `/proc/cmdline`. Use this only for what the agent needs during boot.

## Versioning

- The protocol has one version number. The host boots only a guest image whose manifest carries the same number, and `hello` checks the running agent again.
- A change to a message, the framing, the ports or the guest arguments is a protocol change. Bump `Version` in `internal/proto` and `AGENT_PROTOCOL` in the Makefile together, and rebuild the guest image.
- The kernel's fsevent device has its own ABI number in the guest manifest (`FSEVENT_ABI` in the Makefile). Changing the record format in the kernel patch means bumping it.
- Guest images are versioned by build time and installed side by side; the newest matching one wins.

## The control API is something else

`~/.barely/control.sock` is HTTP between the CLI and the host process: status, top, stop, restart and shell. It never reaches the guest directly, and it carries no version number.
