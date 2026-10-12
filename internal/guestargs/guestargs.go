// Package guestargs is what the host process tells the guest at boot, on the
// kernel command line: the shared roots to mount and the Docker socket
// aliases. The agent needs the shares before dockerd starts (a restarting
// container's bind mounts must work from its first start), which is before
// the control channel could deliver them; the aliases travel the same way
// and are applied once dockerd answers.
//
// Entry points: Args.Encode on the host, Parse in the agent.
//
// Used by: internal/serve, cmd/barely-agent.
package guestargs

import (
	"net/url"
	"strings"

	"barely/internal/vmtypes"
)

// Prefixes of the barely-owned words on the kernel command line.
const (
	shareParam = "barely.share="
	sockParam  = "barely.docksock="
)

// Args is the boot-time configuration the host passes through the kernel
// command line. Shares are the virtio-fs shares to mount, at the same path as
// on the Mac.
type Args struct {
	Shares []vmtypes.Share
	// DockerSockAliases are the Mac-side paths of barely's Docker socket. A
	// tool like testcontainers bind-mounts the socket path it uses on the
	// Mac into a container; inside the VM that path is the Mac's socket
	// file seen through virtio-fs, which Linux can't connect to, so the
	// agent bind-mounts dockerd's socket over it in the guest's view.
	DockerSockAliases []string
}

// Encode returns the parameters to append to the kernel command line. Paths
// are escaped so they're single space-free words.
func (a Args) Encode() string {
	var words []string
	for _, s := range a.Shares {
		words = append(words, shareParam+s.Tag+","+url.PathEscape(s.HostPath))
	}
	for _, p := range a.DockerSockAliases {
		words = append(words, sockParam+url.PathEscape(p))
	}
	return strings.Join(words, " ")
}

// Parse reads Args back out of a whole kernel command line
// (/proc/cmdline), ignoring everything that isn't ours.
func Parse(cmdline string) Args {
	var a Args
	for _, w := range strings.Fields(cmdline) {
		switch {
		case strings.HasPrefix(w, sockParam):
			if p, err := url.PathUnescape(strings.TrimPrefix(w, sockParam)); err == nil {
				a.DockerSockAliases = append(a.DockerSockAliases, p)
			}
		case strings.HasPrefix(w, shareParam):
			tag, path, ok := strings.Cut(strings.TrimPrefix(w, shareParam), ",")
			if !ok {
				continue
			}
			if p, err := url.PathUnescape(path); err == nil {
				a.Shares = append(a.Shares, vmtypes.Share{HostPath: p, Tag: tag})
			}
		}
	}
	return a
}
