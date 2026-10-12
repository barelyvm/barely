// Package dockerproxy is how `docker` on the Mac reaches the guest's dockerd:
// it splices each client of ~/.barely/docker.sock onto the unix socket the VM
// process bridges to vsock port 2375 (run/v2375). No HTTP parsing: hijacked
// streams (attach, exec, BuildKit sessions) need the raw bytes and correct
// half-close.
//
// Used by: internal/serve, once per accepted docker.sock client (Splice).
package dockerproxy

import (
	"fmt"
	"io"
	"net"
	"time"
)

// halfCloser is what *net.UnixConn offers; a half-close is what lets one
// direction of an attach/exec stream end while the other keeps flowing.
type halfCloser interface {
	CloseRead() error
	CloseWrite() error
}

// Splice connects client to v2375Path (retrying while the VM is still
// booting, up to dialTimeout) and copies both directions, propagating
// half-close so hijacked streams end correctly. It returns once both
// directions have finished.
func Splice(client net.Conn, v2375Path string, dialTimeout time.Duration) error {
	guest, err := dialRetry(v2375Path, dialTimeout)
	if err != nil {
		return fmt.Errorf("dockerproxy: %w", err)
	}
	defer guest.Close()

	done := make(chan struct{}, 2)
	go func() {
		io.Copy(guest, client)
		halfClose(guest, client)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(client, guest)
		halfClose(client, guest)
		done <- struct{}{}
	}()
	<-done
	<-done
	return nil
}

// halfClose propagates EOF on src -> dst as a write-close on dst. A conn
// without half-close support is closed fully, which is the only way left to
// signal EOF to the peer; src is unused. Both ends are *net.UnixConn in
// practice, so the fallback is not expected to run.
func halfClose(dst, _ net.Conn) {
	if hc, ok := dst.(halfCloser); ok {
		hc.CloseWrite()
	} else {
		dst.Close()
	}
}

// dialRetry waits for run/v2375 to accept: right after a cold boot the
// socket may not exist yet, or the guest's docker proxy may not be listening
// behind it.
func dialRetry(path string, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		conn, err := net.Dial("unix", path)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("dialing %s: %w", path, lastErr)
		}
		time.Sleep(100 * time.Millisecond) // fast enough that cold start isn't padded, slow enough not to spin
	}
}
