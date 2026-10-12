# Forks

barely builds against forks of libkrun and gvisor-tap-vsock. They are separate repos, not part of this one. Each fork's work happens on its `barely` branch in its own clone.

## How each is pinned

- **libkrun is pinned in `libkrun.env`.** `make libkrun` fetches that commit into `out/libkrun` and builds it. To build a working copy instead, run `make libkrun LIBKRUN_DIR=<path to the clone>`; the last line of output says which commit was built and which one is pinned.
- **gvisor-tap-vsock is pinned by the `replace` line in `go.mod`.** To change the pin: `go mod edit -replace github.com/containers/gvisor-tap-vsock=github.com/barelyvm/gvisor-tap-vsock@<commit>`, then `go mod tidy`. To build against a working copy, point the `replace` line at its path, and do not commit that.

## Rules

- **A fork change is two commits.** Commit and push in the fork, then commit the new pin in this repo.
- **The pin must be fetchable.** Never commit a pin the fork's remote does not have; nobody else could build.
- **What you tested must be what is pinned.** After building from a working copy, update the pin to that commit before calling the work done.
- **Keep each fork a short stack on upstream.** One commit per change, with the reason and the detail in the commit message. The fork carries no separate document describing its changes.
- **A fork commit stands on its own.** Its comments and message describe the change in the fork's own terms and do not mention barely.

## What the forks are based on

- libkrun sits on an upstream release tag, which lives on upstream's stable branch for that release series, not on its main branch. Compare against the stable branch to see only barely's commits.
- gvisor-tap-vsock sits on upstream's main branch.

## What barely needs from them

- **libkrun**: guest memory the kernel reports as free goes back to macOS; a half-closed Docker connection keeps its other direction open, so container output is not lost; a discard never changes the length of the disk image.
- **gvisor-tap-vsock**: guest DNS follows the Mac's network and VPN changes.
