package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"barely/internal/state"
)

// runShell implements "barely shell [-- cmd args...]".
// It boots the VM first if needed (the same way any docker
// call does), then opens a session through the control API's /shell
// hijack, puts the local terminal in raw mode for an interactive session,
// forwards resize, and exits with the guest process's exit code.
func runShell(args []string) error {
	argv := []string{"/bin/sh", "-l"}
	for i, a := range args {
		if a == "--" {
			argv = args[i+1:]
			if len(argv) == 0 {
				argv = []string{"/bin/sh", "-l"}
			}
			break
		}
	}

	paths, err := cliPaths()
	if err != nil {
		return err
	}
	if err := ensureRunning(paths); err != nil {
		return fmt.Errorf("barely shell: %w", err)
	}

	tty := term.IsTerminal(int(os.Stdin.Fd()))
	rows, cols := 24, 80
	if tty {
		if c, r, err := term.GetSize(int(os.Stdin.Fd())); err == nil {
			cols, rows = c, r
		}
	}

	conn, err := net.DialTimeout("unix", paths.ControlSock(), 10*time.Second)
	if err != nil {
		return fmt.Errorf("barely shell: %w", err)
	}
	defer conn.Close()

	reqBody, err := json.Marshal(struct {
		Argv []string `json:"argv"`
		TTY  bool     `json:"tty"`
		Rows int      `json:"rows"`
		Cols int      `json:"cols"`
		Env  []string `json:"env"`
	}{Argv: argv, TTY: tty, Rows: rows, Cols: cols, Env: guestEnv()})
	if err != nil {
		return err
	}
	fmt.Fprintf(conn, "POST /shell HTTP/1.1\r\nHost: barely\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n", len(reqBody))
	if _, err := conn.Write(reqBody); err != nil {
		return fmt.Errorf("barely shell: %w", err)
	}

	br := bufio.NewReader(conn)
	session, err := readShellUpgrade(br)
	if err != nil {
		return fmt.Errorf("barely shell: %w", err)
	}

	var oldState *term.State
	if tty {
		oldState, err = term.MakeRaw(int(os.Stdin.Fd()))
		if err == nil {
			defer term.Restore(int(os.Stdin.Fd()), oldState)
		}
		go watchResize(paths, session)
	}

	// The session is over once the output side sees EOF — the remote
	// closing once the guest process exits — not when input does: a
	// one-off command's stdin is already at EOF immediately (nothing to
	// wait for there), and an interactive session's local stdin never
	// EOFs on its own. Input runs independently and half-closes on its
	// own EOF (rather than closing conn outright) so a one-off command
	// still gets to read whatever output was already in flight.
	go func() {
		io.Copy(conn, os.Stdin)
		if cw, ok := conn.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
	}()
	io.Copy(os.Stdout, br)

	code := fetchShellExit(paths, session)
	if oldState != nil {
		term.Restore(int(os.Stdin.Fd()), oldState)
	}
	os.Exit(code)
	return nil
}

// guestEnv is the environment for the guest process. The Mac's own
// environment (PATH with /opt/homebrew, HOME under /Users, ...) is wrong
// inside Linux, so only terminal settings cross over.
func guestEnv() []string {
	env := []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root"}
	for _, k := range []string{"TERM", "LANG", "LC_ALL"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// readShellUpgrade reads the "HTTP/1.1 200 OK\r\nX-Barely-Session:
// N\r\n\r\n" upgrade response handleShell writes after hijacking, and
// returns the session id.
func readShellUpgrade(br *bufio.Reader) (int, error) {
	statusLine, err := br.ReadString('\n')
	if err != nil {
		return 0, err
	}
	statusLine = string(bytes.TrimRight([]byte(statusLine), "\r\n"))
	ok := strings.HasPrefix(statusLine, "HTTP/1.1 200")

	session := -1
	contentLength := -1
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return 0, err
		}
		line = string(bytes.TrimRight([]byte(line), "\r\n"))
		if line == "" {
			break
		}
		if n, err := fmt.Sscanf(line, "X-Barely-Session: %d", &session); err == nil && n == 1 {
			continue
		}
		fmt.Sscanf(line, "Content-Length: %d", &contentLength)
	}
	if !ok {
		// A failed request isn't hijacked, so the control API's http.Server
		// keeps the connection open for reuse (HTTP/1.1 keep-alive):
		// reading until EOF here would block forever. Read exactly the
		// error body's Content-Length instead.
		var body []byte
		if contentLength > 0 {
			body = make([]byte, contentLength)
			io.ReadFull(br, body)
		}
		return 0, fmt.Errorf("%s: %s", statusLine, bytes.TrimSpace(body))
	}
	if session < 0 {
		return 0, fmt.Errorf("no X-Barely-Session header in response")
	}
	return session, nil
}

// watchResize forwards local terminal resizes as a shell/{id}/resize
// control-API call.
func watchResize(paths state.Paths, session int) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, sigWinch)
	for range ch {
		cols, rows, err := term.GetSize(int(os.Stdin.Fd()))
		if err != nil {
			continue
		}
		body, _ := json.Marshal(struct{ Rows, Cols int }{Rows: rows, Cols: cols})
		client := controlHTTP(paths)
		resp, err := client.Post("http://barely/shell/"+strconv.Itoa(session)+"/resize", "application/json", bytes.NewReader(body))
		if err == nil {
			resp.Body.Close()
		}
	}
}

// fetchShellExit calls GET /shell/{id}/exit, blocking until the session's
// process has exited.
func fetchShellExit(paths state.Paths, session int) int {
	client := controlHTTPNoTimeout(paths)
	resp, err := client.Get("http://barely/shell/" + strconv.Itoa(session) + "/exit")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	var body struct{ Code int }
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 1
	}
	return body.Code
}
