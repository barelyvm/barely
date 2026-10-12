// Package proto is the wire protocol between the host process (barely serve)
// and barely-agent, the guest's init. It owns the vsock port numbers, the
// length-prefixed JSON framing of the control channel on port 1024, the
// message types and payloads, and the session header that opens a shell
// stream on port 1025.
//
// Entry points: Write, Read, New and Message.Decode for framing; Version
// for the handshake.
//
// Used by: internal/control and internal/serve on the host, internal/fswatch
// for FsEvent, cmd/barely-agent in the guest, cmd/barely for guest image checks.
package proto

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Version is the agent protocol version. The host and the guest's agent must
// agree exactly: guestimg refuses images whose manifest agent_protocol
// differs, and the hello exchange re-checks the running agent. Bump it when
// a message or payload changes incompatibly, and keep the Makefile's
// AGENT_PROTOCOL equal so `make guest` stamps the manifest to match.
const Version = 3

// Guest vsock ports. The guest agent listens on each; the host reaches them
// by connecting to the unix sockets run/v1024, v1025 and v2375 (PortDocker is
// the agent's proxy to dockerd).
const (
	PortControl = 1024
	PortShell   = 1025
	PortDocker  = 2375
)

// Frames larger than this are rejected so a corrupt length prefix can't make
// either side allocate unbounded memory.
const MaxFrame = 4 << 20

// Message types. Requests carry a non-zero ID and get a reply of the same
// type: hello, shutdown, set_time, fstrim, shell_open, shell_resize and
// fs_events, all sent by the host. The guest sends ready, mem_stats and
// shell_exit unsolicited with ID 0.
const (
	TypeHello    = "hello"
	TypeReady    = "ready"
	TypeShutdown = "shutdown"
	TypeMemStats = "mem_stats"
	TypeFsEvents = "fs_events"
	// TypeFsRescan has no sender and no handler on either side.
	TypeFsRescan    = "fs_rescan"
	TypeSetTime     = "set_time"
	TypeFstrim      = "fstrim"
	TypeShellOpen   = "shell_open"
	TypeShellResize = "shell_resize"
	TypeShellExit   = "shell_exit"
)

// Message is one frame. Requests carry a non-zero ID and replies echo it;
// unsolicited messages use ID 0. Payload holds the type-specific fields.
type Message struct {
	Type    string          `json:"type"`
	ID      uint64          `json:"id"`
	Error   string          `json:"error,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Hello carries the host's protocol version. On a mismatch the agent still
// answers with its own version, then drops the connection.
type Hello struct {
	Protocol int `json:"protocol"`
}

// HelloReply carries the agent's protocol version and the guest image
// version, so a mismatch error can say which image to rebuild.
type HelloReply struct {
	Protocol     int    `json:"protocol"`
	GuestVersion string `json:"guest_version"`
}

// Ready is sent by the agent once dockerd answers on its socket.
type Ready struct {
	Dockerd bool `json:"dockerd"`
}

// SetTime is sent on wake to resync the guest clock.
type SetTime struct {
	UnixNS int64 `json:"unix_ns"`
}

// FstrimReply is the agent's reply to "fstrim".
type FstrimReply struct {
	TrimmedBytes uint64 `json:"trimmed_bytes"`
}

// FsEvent is one host file change for the guest's fsevent device: Op is
// "C" (created), "M" (contents changed) or "D" (deleted); a rename is D of
// the old path plus C of the new. Path is absolute and the same on both
// sides (shared roots are mounted at identical paths).
type FsEvent struct {
	Op   string `json:"op"`
	Path string `json:"path"`
}

// FsEvents is the payload of "fs_events".
type FsEvents struct {
	Events []FsEvent `json:"events"`
}

// ShellOpen asks the agent to start a shell/command session. The agent spawns the
// process (with a PTY if TTY) and accepts the session's stream on vsock
// port 1025.
type ShellOpen struct {
	Session int      `json:"session"`
	Argv    []string `json:"argv"`
	TTY     bool     `json:"tty"`
	Rows    int      `json:"rows"`
	Cols    int      `json:"cols"`
	Env     []string `json:"env"`
}

// ShellResize resizes an open session's PTY.
type ShellResize struct {
	Session int `json:"session"`
	Rows    int `json:"rows"`
	Cols    int `json:"cols"`
}

// ShellExit is the agent's unsolicited report that a shell session's
// process exited.
type ShellExit struct {
	Session int `json:"session"`
	Code    int `json:"code"`
}

// Write sends m as one frame: a 4-byte big-endian length, then the JSON body.
// The two are separate writes, so writers sharing a connection must
// serialize their calls or frames interleave.
func Write(w io.Writer, m Message) error {
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(body) > MaxFrame {
		return fmt.Errorf("proto: frame of %d bytes exceeds %d", len(body), MaxFrame)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

// Read returns the next frame. A clean EOF before a frame starts is returned
// as io.EOF so callers can tell a closed connection from a torn one.
func Read(r io.Reader) (Message, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Message{}, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > MaxFrame {
		return Message{}, fmt.Errorf("proto: frame of %d bytes exceeds %d", n, MaxFrame)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		// The header arrived, so EOF here is a truncated frame, not a clean close.
		if errors.Is(err, io.EOF) {
			return Message{}, io.ErrUnexpectedEOF
		}
		return Message{}, err
	}
	var m Message
	if err := json.Unmarshal(body, &m); err != nil {
		return Message{}, fmt.Errorf("proto: bad frame: %w", err)
	}
	return m, nil
}

// WriteSessionHeader writes the 4-byte big-endian session id that the host
// process sends first on a new vsock-1025 connection, so the agent's
// accept loop (which otherwise has no other way to tell session streams
// apart) can match it to the PTY opened for that shell_open.
func WriteSessionHeader(w io.Writer, session uint32) error {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], session)
	_, err := w.Write(hdr[:])
	return err
}

// ReadSessionHeader reads the header WriteSessionHeader wrote.
func ReadSessionHeader(r io.Reader) (uint32, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(hdr[:]), nil
}

// New builds a message with payload p marshalled into Payload.
func New(typ string, id uint64, p any) (Message, error) {
	m := Message{Type: typ, ID: id}
	if p == nil {
		return m, nil
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return Message{}, err
	}
	m.Payload = raw
	return m, nil
}

// Decode unmarshals m's payload into p.
func (m Message) Decode(p any) error {
	if len(m.Payload) == 0 {
		return fmt.Errorf("proto: %s has no payload", m.Type)
	}
	return json.Unmarshal(m.Payload, p)
}
