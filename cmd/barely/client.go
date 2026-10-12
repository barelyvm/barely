package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"barely/internal/serve"
	"barely/internal/state"
)

// stateNotRunning is the CLI-side state for "no host process to ask".
const stateNotRunning = "not running"

// cliPaths resolves the state directory the way "barely serve" does when it
// isn't given -state-dir (BARELY_HOME, else ~/.barely).
func cliPaths() (state.Paths, error) {
	dir, err := state.Dir()
	if err != nil {
		return state.Paths{}, err
	}
	return state.New(dir), nil
}

// controlHTTP returns an http.Client that dials control.sock.
func controlHTTP(paths state.Paths) *http.Client {
	sock := paths.ControlSock()
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.DialTimeout("unix", sock, 5*time.Second)
			},
		},
		Timeout: 10 * time.Second,
	}
}

// controlHTTPNoTimeout is for requests that legitimately block a long
// time: POST /stop (answers once the VM is down), GET /shell/{id}/exit (waits
// for the guest process to exit) and the /top stream.
func controlHTTPNoTimeout(paths state.Paths) *http.Client {
	sock := paths.ControlSock()
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.DialTimeout("unix", sock, 5*time.Second)
			},
		},
	}
}

// fetchStatus calls GET /status. If the host process isn't running (the
// usual reason the dial fails: control.sock refused or missing), it
// returns a synthetic stateNotRunning status instead of an error, without
// starting anything.
func fetchStatus(paths state.Paths) (serve.StatusResponse, error) {
	client := controlHTTP(paths)
	resp, err := client.Get("http://barely/status")
	if err != nil {
		return serve.StatusResponse{State: stateNotRunning}, nil
	}
	defer resp.Body.Close()
	var st serve.StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return serve.StatusResponse{}, fmt.Errorf("decoding /status: %w", err)
	}
	return st, nil
}

// controlPost POSTs path with no body for the control endpoints that only
// act (restart); a status of 400 or above becomes an error carrying the
// response body, and a success body is discarded.
func controlPost(paths state.Paths, path string) error {
	return controlPostWith(controlHTTP(paths), path)
}

// controlPostWith is controlPost with the caller's client, for endpoints like
// /stop that outlast controlHTTP's timeout.
func controlPostWith(client *http.Client, path string) error {
	resp, err := client.Post("http://barely"+path, "application/json", nil)
	if err != nil {
		return fmt.Errorf("barely: host process isn't running (or isn't responding): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("barely: %s: %s", path, body)
	}
	return nil
}
