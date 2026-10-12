// Package dockerproxy splices Docker clients on ~/.barely/docker.sock to the
// guest's dockerd over vsock port 2375 (host side: run/v2375). No HTTP parsing: hijacked
// streams (attach, exec, BuildKit sessions) need the raw bytes and correct
// half-close.
package dockerproxy

import (
	"fmt"
	"io"
	"net"
	"time"
)

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

// halfClose propagates EOF on src -> dst as a write-close on dst, if both
// sides support it (they're both *net.UnixConn here).
func halfClose(dst, _ net.Conn) {
	if hc, ok := dst.(halfCloser); ok {
		hc.CloseWrite()
	} else {
		dst.Close()
	}
}

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
		time.Sleep(100 * time.Millisecond)
	}
}
