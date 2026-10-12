package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"barely/internal/state"
)

// runStart implements "barely start": there's no start endpoint
// — it boots the VM the same way any docker call does, by
// connecting to docker.sock, which (via launchd socket activation) starts
// the host process if it isn't already running.
func runStart(args []string) error {
	paths, err := cliPaths()
	if err != nil {
		return err
	}
	if err := ensureRunning(paths); err != nil {
		return err
	}
	fmt.Println("running")
	return nil
}

// ensureRunning boots the VM if needed by pinging docker.sock and returns
// the host process's error message if the boot fails.
func ensureRunning(paths state.Paths) error {
	conn, err := net.DialTimeout("unix", paths.DockerSock(), 10*time.Second)
	if err != nil {
		return fmt.Errorf("barely start: can't reach %s (%v); has `barely setup` been run?", paths.DockerSock(), err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "GET /_ping HTTP/1.1\r\nHost: barely\r\nConnection: close\r\n\r\n"); err != nil {
		return fmt.Errorf("barely start: %w", err)
	}
	// The host process boots the VM before it proxies the first request on
	// docker.sock, so this wait covers a cold boot.
	conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return fmt.Errorf("barely start: no answer from the host process: %w; see `barely logs`", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &e) == nil && e.Message != "" {
			return errors.New(e.Message)
		}
		return fmt.Errorf("barely start: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

// runStop implements "barely stop": POST /stop (clean shutdown now).
func runStop(args []string) error {
	paths, err := cliPaths()
	if err != nil {
		return err
	}
	// /stop answers once the guest has shut down, which can take longer than
	// the default control timeout.
	if err := controlPostWith(controlHTTPNoTimeout(paths), "/stop"); err != nil {
		return err
	}
	fmt.Println("stopped")
	return nil
}

// runRestart implements "barely restart": POST /restart (clean shutdown,
// then boot again with the config reloaded).
func runRestart(args []string) error {
	paths, err := cliPaths()
	if err != nil {
		return err
	}
	if err := controlPost(paths, "/restart"); err != nil {
		return err
	}
	fmt.Println("restarted")
	return nil
}

// logsFlags holds "barely logs"'s flags; newLogsFlags is also used by
// "barely help logs" and "barely man" so the flag list can never drift from
// what logs actually parses.
type logsFlags struct {
	fs     *flag.FlagSet
	source *string
	follow *bool
}

func newLogsFlags() *logsFlags {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	f := &logsFlags{fs: fs}
	f.source = fs.String("source", "serve", "serve, vm, or console")
	f.follow = fs.Bool("follow", false, "keep reading as the file grows")
	return f
}

// runLogs implements "barely logs": reads the files in logs/
// directly when the host process isn't running, and the same files
// either way, since the host's own GET /logs just reads them too.
func runLogs(args []string) error {
	flags := newLogsFlags()
	if err := flags.fs.Parse(args); err != nil {
		return err
	}
	source := flags.source
	follow := flags.follow

	paths, err := cliPaths()
	if err != nil {
		return err
	}
	var path string
	switch *source {
	case "serve":
		path = paths.ServeLog()
	case "vm":
		path = paths.VMLog()
	case "console":
		path = paths.ConsoleLog()
	default:
		return fmt.Errorf("barely logs: unknown -source %q", *source)
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("barely logs: %w", err)
	}
	defer f.Close()
	io.Copy(os.Stdout, f)
	if !*follow {
		return nil
	}
	for {
		time.Sleep(500 * time.Millisecond)
		io.Copy(os.Stdout, f)
	}
}
