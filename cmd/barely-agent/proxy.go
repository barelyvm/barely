package main

import (
	"io"
	"log"
	"net"
	"time"

	"github.com/mdlayher/vsock"

	"barely/internal/proto"
)

const (
	dockerSocket = "/run/docker.sock"
	// dockerDialTimeout bounds how long a client waits for dockerd to start
	// listening; see spliceToDocker.
	dockerDialTimeout = 60 * time.Second
)

// serveDockerProxy accepts connections on vsock port 2375 and splices each
// one to dockerd's Unix socket, propagating half-close so hijacked streams
// (attach, exec, BuildKit sessions) end correctly.
func (a *agent) serveDockerProxy() error {
	ln, err := vsock.Listen(proto.PortDocker, nil)
	if err != nil {
		return err
	}
	defer ln.Close()
	log.Printf("listening on vsock port %d (docker proxy)", proto.PortDocker)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go spliceToDocker(conn)
	}
}

func spliceToDocker(client net.Conn) {
	defer client.Close()
	// The vsock listener comes up before dockerd, so the host's first
	// client can arrive while dockerd is still starting.
	deadline := time.Now().Add(dockerDialTimeout)
	dsock, err := net.Dial("unix", dockerSocket)
	for err != nil && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		dsock, err = net.Dial("unix", dockerSocket)
	}
	if err != nil {
		log.Printf("proxy: dial %s: %v", dockerSocket, err)
		return
	}
	defer dsock.Close()

	done := make(chan struct{}, 2)
	go func() {
		io.Copy(dsock, client)
		closeWrite(dsock)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(client, dsock)
		closeWrite(client)
		done <- struct{}{}
	}()
	<-done
	<-done
}

type closeWriter interface {
	CloseWrite() error
}

// closeWrite half-closes conn's write side once its read side has hit EOF,
// so the peer sees EOF instead of hanging on a full-duplex stream.
func closeWrite(conn net.Conn) {
	if cw, ok := conn.(closeWriter); ok {
		cw.CloseWrite()
		return
	}
	conn.Close()
}
