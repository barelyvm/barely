// Package proto is the control channel spoken between barely serve and barely-agent
// over guest vsock port 1024.
package proto

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const Version = 3

const (
	PortControl = 1024
	PortShell   = 1025
	PortDocker  = 2375
)

// Frames larger than this are rejected so a corrupt length prefix can't make
// either side allocate unbounded memory.
const MaxFrame = 4 << 20

const (
	TypeHello       = "hello"
	TypeReady       = "ready"
	TypeShutdown    = "shutdown"
	TypeMemStats    = "mem_stats"
	TypeFsEvents    = "fs_events"
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

type Hello struct {
	Protocol int `json:"protocol"`
}

type HelloReply struct {
	Protocol     int    `json:"protocol"`
	GuestVersion string `json:"guest_version"`
}

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
