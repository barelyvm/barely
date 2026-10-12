# barely

barely is an open source Docker host VM with elastic memory and storage
management.

Docker on a Mac needs a Linux VM, and that VM usually holds a fixed share
of the machine whether containers need it or not. On a 16 GB laptop with a
512 GB disk that share is hard to spare. barely's VM takes memory and disk
space as containers use them and hands them back to macOS afterwards:

- **Memory**: the VM grows up to a cap (half the Mac's RAM by default) and
  shrinks again when a build or container finishes. With nothing running
  it shows about 500 MB in Activity Monitor.
- **Storage**: images, volumes and build cache live in one disk file that
  takes only what is written, and gets smaller again after you delete
  things.

It runs the real Docker engine, so `docker`, `docker compose` and tools
that talk to the Docker socket work unchanged.

## Install from source

Requirements: an Apple Silicon Mac on macOS 14+, Xcode command line tools,
[Homebrew](https://brew.sh), Go, a Rust toolchain via [rustup](https://rustup.rs),
and Docker (any engine) to build the guest image.

```sh
brew install go rustup lld xz docker docker-compose docker-buildx
rustup-init -y
brew install --cask docker  # or: brew install docker-desktop; start it before make kernel
make libkrun   # fetches and builds the libkrun fork pinned in libkrun.env
make barelyvm    # builds and ad-hoc signs the VM helper
make kernel    # builds the guest kernel
make guest     # builds the guest image (kernel, Alpine root, barely-agent)
```

Only `barelyvm` touches Apple's Hypervisor framework, so it's the only binary
that needs signing; `make barelyvm` ad-hoc signs it with the
`com.apple.security.hypervisor`
entitlement (`barely.entitlements`). No Apple developer account is needed.
`barely` itself needs no signing, so `go run ./cmd/barely …` works for every
command, setup included. `barely setup` installs `barely`, `barelyvm` and
`libkrun.1.dylib` into `~/.barely/bin` and points launchd there, so rebuilding
in the repo never touches a running install; re-run `barely setup` to update it.
Add `~/.barely/bin` to your `PATH` to run `barely` from anywhere.

## Setup

```sh
go run ./cmd/barely setup   # or: make barely && ./out/bin/barely setup
```

Setup walks through seven steps, saying what each one changes on your Mac
before it does it, and asking wherever there's a choice:

1. **Check what barely needs**: the docker CLI, its compose and buildx plugins, any credential helper your Docker config names. Each missing piece comes with the command that installs it.
2. **Install barely** into `~/.barely/bin` (barely, the signed `barelyvm` helper, libkrun).
3. **Memory and disk caps**: asks how far the VM may grow (Enter keeps the defaults: half the Mac's RAM, 100G of disk). Sizes are typed like `12G` or `512M`. The disk cap can be raised later, not lowered.
4. **Start on demand**: a per-user launchd agent that owns `~/.barely/docker.sock`; nothing runs until the first connection. Asks whether to also boot at login.
5. **Docker context**: creates a `barely` context and asks whether to make it current.
6. **Default socket path**: asks whether to symlink `/var/run/docker.sock` to barely's socket (one `sudo`), for tools that open that path directly. If it belongs to another tool, it asks before replacing it.
7. **Shell** (optional): asks whether to add `export DOCKER_HOST=…` to your zsh, bash or fish profile, which pins terminals to barely whatever the current context is.

A choice given as a flag (`barely help setup` lists them) isn't asked; with
`-defaults` setup asks nothing and every choice you didn't specify takes its
default. `barely uninstall` reverses all of it.

After setup, plain `docker` commands just work:

```sh
docker run --rm hello-world
```

The first call boots the VM (a few seconds, cold); after
`idle_timeout` (5 minutes by default) with no open clients and no running
containers, every `barely` process exits — the next `docker` call starts
everything again.

## CLI

| Command | What it does |
|---|---|
| `barely status` | VM state, memory footprint now vs recent peak, guest used/cache, disk |
| `barely top` | Live 1s-refreshed view of the above, plus a footprint sparkline and per-container memory/CPU |
| `barely shell [-- cmd args…]` | Root shell (or one-off command) in the guest, no SSH |
| `barely start` / `barely stop` / `barely restart` | Boot, clean shutdown, or shutdown+reboot (applies config) |
| `barely logs -source serve\|vm\|console [-follow]` | Host process log, VM process log, or guest console |
| `barely doctor` | `/var/run/docker.sock` target, active Docker context, Docker Desktop VM status, CLI checks |
| `barely config [edit]` | Print or edit `~/.barely/config.toml` |
| `barely uninstall [-purge]` | Reverses `setup` and removes `~/.barely/bin`; `-purge` also deletes the rest of `~/.barely` |

Run `barely help` for the full list (grouped, with pointers to `barely help <command>`), or `barely help <command>` / `barely <command> -h` for a command's flags and examples.

A man page is generated from the same help text: `make man` writes `out/man/barely.1`, which you can view with `man ./out/man/barely.1` without installing anything, or install by copying it into a directory on your `MANPATH` (e.g. `cp out/man/barely.1 /usr/local/share/man/man1/`).

## Config

`~/.barely/config.toml` is the only file you edit (`barely config edit`):

```toml
memory_max = "8G"      # default: half of host RAM
cpus = 0               # default: all cores
disk_max = "100G"      # can be raised later, not lowered
idle_timeout = "5m"    # 0 disables idle shutdown
shares = ["/Users", "/Volumes", "/private", "/tmp", "/var/folders"]
start_at_login = false
```

Changes apply on the next VM boot, or immediately with `barely restart`.

## Uninstall

```sh
barely uninstall           # reverses setup, removes ~/.barely/bin; keeps images, volumes, logs, config
barely uninstall -purge    # also deletes the rest of ~/.barely
```

## Running alongside Docker Desktop

`barely` and Docker Desktop can both be installed and running: separate
sockets, contexts, images, containers and volumes. `barely doctor` reports
which one currently owns `/var/run/docker.sock` and the active Docker
context; use `docker --context barely …` or `--context desktop-linux …` to
pick one explicitly. If you run both regularly, set Docker Desktop's
memory/CPU sliders to the minimum and turn on its Resource Saver so it
also gives memory back when idle.

## Known limitations

- Apple Silicon and macOS 14+ only. No Kubernetes.
- amd64 images are not supported, only arm64.
- A `touch` that only changes a file's timestamp raises no event in containers. Saves and edits do.
- Launching Docker Desktop makes `desktop-linux` the current Docker context and can retake `/var/run/docker.sock`; `barely doctor` shows who owns them, `docker context use barely` switches back.
- Images, containers and volumes are separate from Docker Desktop's.
- SSH agent forwarding into builds, IPv6 inside the VM, and reaching container IPs directly from the Mac aren't implemented.
- A long-lived process that keeps its own memory (for example Node after a large build) holds the VM's footprint up until the process releases it.

## License

barely is licensed under the Apache License, Version 2.0 (see [LICENSE](LICENSE)). The patches in `kernel/patches/` modify the Linux kernel and are licensed under the GNU General Public License, version 2.
