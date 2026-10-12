package main

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"

	"barely/internal/proto"
)

// A one-off command's output only EOFs once the agent's splice (which
// waits on both directions, like the Docker proxy's) sees the client side
// close too — which, over the real vsock stream, the host does once it
// relays shell_exit to the CLI. This test plays the host's part: it reads
// the process's output concurrently, waits for shell_exit on the control
// pipe, then closes the session stream, same as the host would.
func TestShellOneOffCommand(t *testing.T) {
	ctlConn, ctlPeer := net.Pipe()
	defer ctlConn.Close()
	defer ctlPeer.Close()
	conn := &controlConn{Conn: ctlConn}

	a := &agent{}
	req := proto.ShellOpen{Session: 1, Argv: []string{"/bin/echo", "hi"}, TTY: false}
	if err := a.startShell(conn, req); err != nil {
		t.Fatalf("startShell: %v", err)
	}

	streamConn, streamPeer := net.Pipe()
	go handleShellConn(streamConn)

	if err := proto.WriteSessionHeader(streamPeer, uint32(req.Session)); err != nil {
		t.Fatalf("writing session header: %v", err)
	}

	var out bytes.Buffer
	outDone := make(chan struct{})
	go func() {
		io.Copy(&out, streamPeer)
		close(outDone)
	}()

	ctlPeer.SetReadDeadline(time.Now().Add(5 * time.Second))
	m, err := proto.Read(ctlPeer)
	if err != nil {
		t.Fatalf("reading shell_exit: %v", err)
	}
	if m.Type != proto.TypeShellExit {
		t.Fatalf("got message type %q, want %q", m.Type, proto.TypeShellExit)
	}
	var exit proto.ShellExit
	if err := m.Decode(&exit); err != nil {
		t.Fatal(err)
	}
	if exit.Session != req.Session || exit.Code != 0 {
		t.Fatalf("got %+v, want session %d code 0", exit, req.Session)
	}

	streamPeer.Close()
	select {
	case <-outDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for session stream to close")
	}
	if out.String() != "hi\n" {
		t.Fatalf("got output %q, want %q", out.String(), "hi\n")
	}
}

func TestShellResizeUnknownSession(t *testing.T) {
	if err := resizeShell(99999, 24, 80); err == nil {
		t.Fatal("expected error for unknown session")
	}
}
