// Package control is the host side of the control channel to barely-agent,
// vsock port 1024 (host side: run/v1024). It is how barely serve learns the
// guest is up (hello, ready), watches its memory (mem_stats), and drives
// the guest from outside: shutdown, clock resync, fstrim, host file events
// and shell sessions.
//
// Entry points: DialAndHello to connect, then the Client methods.
//
// Used by: internal/serve.
package control

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"barely/internal/proto"
	"barely/internal/vmtypes"
)

// ErrProtocolMismatch marks a Hello failure as a real guest/host protocol
// mismatch, as opposed to a transport error. DialAndHello doesn't retry
// this one: boot should fail with a clear error, not keep reconnecting.
var ErrProtocolMismatch = errors.New("control: guest/host protocol mismatch")

// Client is one connection to the agent. DialAndHello starts its Run
// goroutine, which reads unsolicited messages and replies; Request blocks
// for a reply.
type Client struct {
	conn    net.Conn
	nextID  atomic.Uint64
	mu      sync.Mutex
	pending map[uint64]chan proto.Message
	// writeMu keeps one request's frame whole: proto.Write sends the length
	// and the body as two writes, and requests come from several goroutines.
	writeMu sync.Mutex

	readyMu sync.Mutex
	ready   bool

	memMu sync.Mutex
	mem   vmtypes.MemStats

	shellMu    sync.Mutex
	nextSess   atomic.Int64
	shellExits map[int]chan int
}

func dialOnce(path string) (*Client, error) {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, err
	}
	c := &Client{
		conn:       conn,
		pending:    map[uint64]chan proto.Message{},
		shellExits: map[int]chan int{},
	}
	go c.Run()
	return c, nil
}

// DialAndHello connects to path (run/v1024) and performs the hello
// handshake, retrying the whole thing until timeout. libkrun's host-side
// socket for a "listen" vsock port accepts the connection as soon as the
// VM is configured, independent of whether the guest has actually bound
// and listened on that vsock port yet — so a dial can succeed and then a
// write still fail (broken pipe) while the guest is still booting. Only a
// genuine protocol mismatch (ErrProtocolMismatch) is not retried.
func DialAndHello(path string, timeout, helloAttempt time.Duration) (*Client, proto.HelloReply, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		c, err := dialOnce(path)
		if err == nil {
			reply, helloErr := c.Hello(helloAttempt)
			if helloErr == nil {
				return c, reply, nil
			}
			c.Close()
			if errors.Is(helloErr, ErrProtocolMismatch) {
				return nil, proto.HelloReply{}, helloErr
			}
			lastErr = helloErr
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return nil, proto.HelloReply{}, fmt.Errorf("control: %s: %w", path, lastErr)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// Run reads frames until the connection closes, routing replies to
// Request callers and handling unsolicited messages (ready, mem_stats,
// shell_exit).
func (c *Client) Run() {
	for {
		m, err := proto.Read(c.conn)
		if err != nil {
			c.failPending(err)
			return
		}
		// Replies echo a non-zero ID; unsolicited messages carry 0. A
		// reply nobody is waiting for any more (Request timed out) falls
		// through to the type switch and is dropped there.
		if m.ID != 0 {
			c.mu.Lock()
			ch, ok := c.pending[m.ID]
			if ok {
				delete(c.pending, m.ID)
			}
			c.mu.Unlock()
			if ok {
				ch <- m
				continue
			}
		}
		switch m.Type {
		case proto.TypeReady:
			var r proto.Ready
			if m.Decode(&r) == nil {
				c.readyMu.Lock()
				c.ready = r.Dockerd
				c.readyMu.Unlock()
			}
		case proto.TypeMemStats:
			var ms vmtypes.MemStats
			if m.Decode(&ms) == nil {
				c.memMu.Lock()
				c.mem = ms
				c.memMu.Unlock()
			}
		case proto.TypeShellExit:
			var e proto.ShellExit
			if m.Decode(&e) == nil {
				c.shellMu.Lock()
				ch := c.shellExits[e.Session]
				delete(c.shellExits, e.Session)
				c.shellMu.Unlock()
				if ch != nil {
					ch <- e.Code
				}
			}
		}
	}
}

// failPending wakes every blocked Request when the connection dies, by
// closing its reply channel; Request reads the close as "connection closed".
func (c *Client) failPending(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
}

// Request sends typ/payload and waits for the matching reply, up to
// timeout, or an error if the connection drops first.
func (c *Client) Request(typ string, payload any, reply any, timeout time.Duration) error {
	id := c.nextID.Add(1)
	msg, err := proto.New(typ, id, payload)
	if err != nil {
		return err
	}
	// Buffered so Run can hand over a reply without blocking even if this
	// Request has just timed out and stopped receiving.
	ch := make(chan proto.Message, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	c.writeMu.Lock()
	err = proto.Write(c.conn, msg)
	c.writeMu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	select {
	case resp, ok := <-ch:
		if !ok {
			return fmt.Errorf("control: connection closed waiting for %s reply", typ)
		}
		if resp.Error != "" {
			return fmt.Errorf("control: %s: %s", typ, resp.Error)
		}
		if reply != nil {
			return resp.Decode(reply)
		}
		return nil
	case <-time.After(timeout):
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("control: %s: no reply within %s", typ, timeout)
	}
}

// Hello checks that the running agent speaks proto.Version. guestimg.Find
// already filters images by manifest, so a mismatch here means the image's
// manifest disagrees with the agent built into it; failing at boot beats
// misparsing messages later.
func (c *Client) Hello(timeout time.Duration) (proto.HelloReply, error) {
	var reply proto.HelloReply
	if err := c.Request(proto.TypeHello, proto.Hello{Protocol: proto.Version}, &reply, timeout); err != nil {
		return proto.HelloReply{}, err
	}
	if reply.Protocol != proto.Version {
		return reply, fmt.Errorf("%w: host wants %d, guest speaks %d (guest version %s)", ErrProtocolMismatch, proto.Version, reply.Protocol, reply.GuestVersion)
	}
	return reply, nil
}

// Shutdown asks the agent to stop dockerd and power off.
func (c *Client) Shutdown(timeout time.Duration) error {
	return c.Request(proto.TypeShutdown, struct{}{}, nil, timeout)
}

// Ready reports whether the agent has last reported dockerd as answering.
func (c *Client) Ready() bool {
	c.readyMu.Lock()
	defer c.readyMu.Unlock()
	return c.ready
}

// MemStats returns the most recently reported mem_stats (zero value if
// none have arrived yet), for "status" and "top".
func (c *Client) MemStats() vmtypes.MemStats {
	c.memMu.Lock()
	defer c.memMu.Unlock()
	return c.mem
}

// SetTime resyncs the guest clock, e.g. on Mac wake.
func (c *Client) SetTime(t time.Time, timeout time.Duration) error {
	return c.Request(proto.TypeSetTime, proto.SetTime{UnixNS: t.UnixNano()}, nil, timeout)
}

// Fstrim asks the agent to trim the data disk.
func (c *Client) Fstrim(timeout time.Duration) (uint64, error) {
	var reply proto.FstrimReply
	if err := c.Request(proto.TypeFstrim, struct{}{}, &reply, timeout); err != nil {
		return 0, err
	}
	return reply.TrimmedBytes, nil
}

// NewSession allocates a shell session id and registers its exit channel
// before the caller sends shell_open, so a fast-exiting one-off command
// can't report shell_exit before anyone is listening for it.
func (c *Client) NewSession() (session int, exit <-chan int) {
	id := int(c.nextSess.Add(1))
	ch := make(chan int, 1)
	c.shellMu.Lock()
	c.shellExits[id] = ch
	c.shellMu.Unlock()
	return id, ch
}

// ShellOpen asks the agent to start session's process.
// The caller then dials run/v1025 and sends
// session's id as the header (proto.WriteSessionHeader) to pick up the
// stream.
func (c *Client) ShellOpen(session int, argv []string, tty bool, rows, cols int, env []string, timeout time.Duration) error {
	return c.Request(proto.TypeShellOpen, proto.ShellOpen{
		Session: session, Argv: argv, TTY: tty, Rows: rows, Cols: cols, Env: env,
	}, nil, timeout)
}

// ShellResize resizes an open session's PTY.
func (c *Client) ShellResize(session, rows, cols int, timeout time.Duration) error {
	return c.Request(proto.TypeShellResize, proto.ShellResize{Session: session, Rows: rows, Cols: cols}, nil, timeout)
}

func (c *Client) Close() error { return c.conn.Close() }
