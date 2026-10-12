package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"github.com/mdlayher/vsock"

	"barely/internal/proto"
)

// shellSession is one "barely shell" session:
// a process the agent started on the
// host's request, waiting to be (or already) spliced to the raw stream on
// vsock port 1025.
type shellSession struct {
	cmd *exec.Cmd
	ctl *controlConn
	req proto.ShellOpen

	tty *os.File // set when the session has a PTY (req.TTY)

	// Set when the session has no PTY: a one-off command's output doesn't
	// need terminal semantics, just its stdin/stdout/stderr merged into
	// one stream.
	stdinW  *os.File
	stdoutR *os.File
}

// sessions holds shell_open requests from registration until the process
// exits; shellMu guards it.
var (
	shellMu  sync.Mutex
	sessions = map[int]*shellSession{}
)

// serveShell accepts the host's raw session streams on vsock port 1025.
// Each connection starts with the 4-byte session header
// (proto.WriteSessionHeader) identifying which shell_open it belongs to.
func (a *agent) serveShell() error {
	ln, err := vsock.Listen(proto.PortShell, nil)
	if err != nil {
		return err
	}
	defer ln.Close()
	log.Printf("listening on vsock port %d (shell)", proto.PortShell)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go handleShellConn(conn)
	}
}

// handleShellConn starts the session named by the stream's header and
// splices it to the host, or reports exit 127 if the process won't start.
func handleShellConn(conn net.Conn) {
	defer conn.Close()
	session, err := proto.ReadSessionHeader(conn)
	if err != nil {
		log.Printf("shell: reading session header: %v", err)
		return
	}
	shellMu.Lock()
	sess := sessions[int(session)]
	shellMu.Unlock()
	if sess == nil {
		log.Printf("shell: no session %d (shell_open not received, or already spliced)", session)
		return
	}
	log.Printf("shell: session %d stream connected", session)
	if err := sess.start(); err != nil {
		log.Printf("shell: session %d: %v", session, err)
		fmt.Fprintf(conn, "barely-agent: %v\r\n", err)
		shellMu.Lock()
		delete(sessions, int(session))
		shellMu.Unlock()
		// 127: the shell convention for "command could not be run".
		sess.report(int(session), 127)
		return
	}
	go sess.wait(int(session))
	sess.splice(conn)
	log.Printf("shell: session %d stream disconnected", session)
}

// startShell implements shell_open: it validates argv and registers the
// session. The process starts only once the host's stream connects on
// vsock 1025 (handleShellConn), so a fast command can't finish, and its
// output vanish, before anyone is reading.
func (a *agent) startShell(conn *controlConn, req proto.ShellOpen) error {
	if len(req.Argv) == 0 {
		return fmt.Errorf("shell_open: empty argv")
	}
	if _, err := exec.LookPath(req.Argv[0]); err != nil {
		return fmt.Errorf("shell_open: %w", err)
	}
	shellMu.Lock()
	sessions[req.Session] = &shellSession{ctl: conn, req: req}
	shellMu.Unlock()
	return nil
}

func (s *shellSession) start() error {
	req := s.req
	cmd := exec.Command(req.Argv[0], req.Argv[1:]...)
	if len(req.Env) > 0 {
		cmd.Env = req.Env
	} else {
		cmd.Env = os.Environ()
	}
	s.cmd = cmd
	if req.TTY {
		f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(req.Rows), Cols: uint16(req.Cols)})
		if err != nil {
			return err
		}
		s.tty = f
		return nil
	}
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return err
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		stdinR.Close()
		stdinW.Close()
		return err
	}
	cmd.Stdin = stdinR
	cmd.Stdout = stdoutW
	cmd.Stderr = stdoutW
	if err := cmd.Start(); err != nil {
		stdinR.Close()
		stdinW.Close()
		stdoutR.Close()
		stdoutW.Close()
		return err
	}
	stdinR.Close()
	stdoutW.Close()
	s.stdinW, s.stdoutR = stdinW, stdoutR
	return nil
}

// wait reports the process's exit code as shell_exit once it finishes
// and drops the session.
func (s *shellSession) wait(session int) {
	err := s.cmd.Wait()
	code := exitCode(err)
	log.Printf("shell session %d process exited: code=%d err=%v", session, code, err)

	shellMu.Lock()
	delete(sessions, session)
	shellMu.Unlock()

	if s.stdinW != nil {
		s.stdinW.Close()
	}
	s.report(session, code)
}

func (s *shellSession) report(session, code int) {
	m, err := proto.New(proto.TypeShellExit, 0, proto.ShellExit{Session: session, Code: code})
	if err != nil {
		log.Printf("shell: building shell_exit: %v", err)
		return
	}
	if err := s.ctl.send(m); err != nil {
		log.Printf("shell: sending shell_exit: %v", err)
	}
}

// exitCode maps a Wait error to a status; -1 means the process did not
// exit normally (killed by a signal, or Wait itself failed).
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// splice connects conn to the session's PTY or stdin/stdout pipes. Without
// a PTY, input reaching EOF just closes the process's stdin (a one-off
// command has no input at all) and only a failed write back to the client
// means it's gone; the session ends when the process's output ends. A gone
// client hangs the process up with SIGHUP.
func (s *shellSession) splice(conn net.Conn) {
	var r io.Reader
	var w io.Writer
	if s.tty != nil {
		r, w = s.tty, s.tty
	} else {
		r, w = s.stdoutR, s.stdinW
	}

	go func() {
		io.Copy(w, conn)
		if s.tty == nil {
			s.stdinW.Close()
			return
		}
		// An interactive client's input only ends when the client goes away,
		// so EOF here is a hangup, not a closed stdin.
		if s.cmd.Process != nil {
			s.cmd.Process.Signal(syscall.SIGHUP)
		}
	}()

	_, err := io.Copy(conn, r)
	var pathErr *os.PathError
	// EIO is how a PTY master reports that the process closed its end: the
	// normal end of output, not a lost client.
	clientGone := err != nil && !(errors.As(err, &pathErr) && errors.Is(pathErr.Err, syscall.EIO))
	if clientGone && s.cmd.Process != nil {
		s.cmd.Process.Signal(syscall.SIGHUP)
	}
	if s.tty != nil {
		s.tty.Close()
	}
	if s.stdoutR != nil {
		s.stdoutR.Close()
	}
	if cw, ok := conn.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite()
	}
}

// resizeShell implements shell_resize for a session with a PTY; it's a
// no-op (not an error) for a one-off command, which has none.
func resizeShell(session, rows, cols int) error {
	shellMu.Lock()
	sess := sessions[session]
	shellMu.Unlock()
	if sess == nil {
		return fmt.Errorf("shell_resize: no session %d", session)
	}
	if sess.tty == nil {
		return nil
	}
	return pty.Setsize(sess.tty, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}
