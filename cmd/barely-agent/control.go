package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"sync"

	"github.com/mdlayher/vsock"

	"barely/internal/proto"
)

// agent serves the guest side of the host ↔ agent control channel and the
// Docker socket proxy.
type agent struct {
	version string
}

func newAgent(version string) *agent {
	return &agent{version: version}
}

// dockerReady closes once dockerd answers on its socket (bootstrap.go, PID
// 1 only); the control channel sends the unsolicited "ready" when it does.
var dockerReady = make(chan struct{})

func (a *agent) run() error {
	errCh := make(chan error, 3)
	go func() { errCh <- a.serveControl() }()
	go func() { errCh <- a.serveDockerProxy() }()
	go func() { errCh <- a.serveShell() }()
	return <-errCh
}

// serveControl accepts the host's long-lived connection to vsock port 1024
// and dispatches each request; unknown message types reply with an error.
func (a *agent) serveControl() error {
	ln, err := vsock.Listen(proto.PortControl, nil)
	if err != nil {
		return err
	}
	defer ln.Close()
	log.Printf("listening on vsock port %d (control)", proto.PortControl)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go a.handleControl(conn)
	}
}

// controlConn serializes writes: the read loop replies to requests, and a
// separate goroutine sends the unsolicited "ready" once dockerd answers.
type controlConn struct {
	net.Conn
	mu sync.Mutex
}

func (c *controlConn) send(m proto.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return proto.Write(c, m)
}

func (a *agent) handleControl(nc net.Conn) {
	conn := &controlConn{Conn: nc}
	defer conn.Close()

	go func() {
		if os.Getpid() != 1 {
			return // not bootstrapped: no dockerd to wait on (local test run)
		}
		<-dockerReady
		ready, err := proto.New(proto.TypeReady, 0, proto.Ready{Dockerd: true})
		if err != nil {
			log.Printf("control: build ready: %v", err)
			return
		}
		if err := conn.send(ready); err != nil {
			log.Printf("control: send ready: %v", err)
		}
	}()

	if os.Getpid() == 1 {
		go sendMemStats(conn)
	}

	for {
		msg, err := proto.Read(conn)
		if err != nil {
			return
		}
		if err := a.dispatch(conn, msg); err != nil {
			log.Printf("control: %s: %v", msg.Type, err)
			return
		}
	}
}

func (a *agent) dispatch(conn *controlConn, msg proto.Message) error {
	switch msg.Type {
	case proto.TypeHello:
		var hello proto.Hello
		if err := msg.Decode(&hello); err != nil {
			return a.reply(conn, msg, nil, err)
		}
		helloReply := proto.HelloReply{Protocol: proto.Version, GuestVersion: a.version}
		if hello.Protocol != proto.Version {
			// The host tells a mismatch from this reply's protocol and
			// fails boot; an error reply would look like a
			// transient failure and be retried.
			a.reply(conn, msg, helloReply, nil)
			return fmt.Errorf("protocol mismatch: host speaks %d, guest speaks %d", hello.Protocol, proto.Version)
		}
		return a.reply(conn, msg, helloReply, nil)
	case proto.TypeShutdown:
		stopDockerd()
		if err := a.reply(conn, msg, struct{}{}, nil); err != nil {
			return err
		}
		powerOff()
		return nil
	case proto.TypeSetTime:
		var st proto.SetTime
		if err := msg.Decode(&st); err != nil {
			return a.reply(conn, msg, nil, err)
		}
		return a.reply(conn, msg, struct{}{}, setClock(st.UnixNS))
	case proto.TypeFsEvents:
		var fe proto.FsEvents
		if err := msg.Decode(&fe); err != nil {
			return a.reply(conn, msg, nil, err)
		}
		n, err := applyFsEvents(fe.Events)
		return a.reply(conn, msg, struct {
			Applied int `json:"applied"`
		}{n}, err)
	case proto.TypeFstrim:
		// A trim can take seconds; don't hold up the control channel.
		go func() {
			n, err := fstrimDataDisk()
			if err != nil {
				a.reply(conn, msg, nil, err)
				return
			}
			a.reply(conn, msg, proto.FstrimReply{TrimmedBytes: n}, nil)
		}()
		return nil
	case proto.TypeShellOpen:
		var so proto.ShellOpen
		if err := msg.Decode(&so); err != nil {
			return a.reply(conn, msg, nil, err)
		}
		return a.reply(conn, msg, struct{}{}, a.startShell(conn, so))
	case proto.TypeShellResize:
		var sr proto.ShellResize
		if err := msg.Decode(&sr); err != nil {
			return a.reply(conn, msg, nil, err)
		}
		return a.reply(conn, msg, struct{}{}, resizeShell(sr.Session, sr.Rows, sr.Cols))
	default:
		return a.reply(conn, msg, nil, fmt.Errorf("unknown message type %q", msg.Type))
	}
}

func (a *agent) reply(conn *controlConn, req proto.Message, payload any, replyErr error) error {
	m := proto.Message{Type: req.Type, ID: req.ID}
	if replyErr != nil {
		m.Error = replyErr.Error()
	} else if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		m.Payload = raw
	}
	return conn.send(m)
}
