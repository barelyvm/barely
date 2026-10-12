// This file is the CLI's control API: HTTP+JSON over ~/.barely/control.sock, present only while
// the host process runs.

package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"barely/internal/diskimg"
	"barely/internal/footprint"
	"barely/internal/proto"
	"barely/internal/vmtypes"
)

// FootprintPeak is the highest footprint sample recorded since the VM's
// last boot, and when.
type FootprintPeak struct {
	Bytes uint64    `json:"bytes"`
	At    time.Time `json:"at"`
}

// DiskStatus is the data disk's logical (apparent) vs on-disk (actually
// allocated) size.
type DiskStatus struct {
	Logical uint64 `json:"logical"`
	OnDisk  uint64 `json:"on_disk"`
}

// StatusResponse is GET /status's body.
type StatusResponse struct {
	State                  string           `json:"state"` // "running", "booting" or "stopped"
	Uptime                 time.Duration    `json:"uptime"`
	VMFootprint            uint64           `json:"vm_footprint"`
	VMFootprintPeak        FootprintPeak    `json:"vm_footprint_peak"`
	DockerDesktopFootprint *uint64          `json:"docker_desktop_footprint,omitempty"`
	Guest                  vmtypes.MemStats `json:"guest"`
	Disk                   DiskStatus       `json:"disk"`
	IdleIn                 time.Duration    `json:"idle_in"`
}

// TopEvent is one event of GET /top's server-sent stream.
type TopEvent struct {
	StatusResponse
	History    []footprint.Sample `json:"history,omitempty"` // first event only
	Containers []ContainerStat    `json:"containers"`
}

// ContainerStat is one running container's resource use, from dockerd's
// stats endpoint.
type ContainerStat struct {
	Name string  `json:"name"`
	Mem  uint64  `json:"mem"`
	CPU  float64 `json:"cpu"`
}

// startControlAPI serves the control API on control.sock until ctx is
// cancelled.
func (s *Server) startControlAPI(ctx context.Context) error {
	sockPath := s.paths.ControlSock()
	os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("serve: control API: listening on %s: %w", sockPath, err)
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		ln.Close()
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/top", s.handleTop)
	mux.HandleFunc("/stop", s.handleStop)
	mux.HandleFunc("/restart", s.handleRestart)
	mux.HandleFunc("/shell", s.handleShell)
	mux.HandleFunc("/shell/", s.handleShellResize)
	mux.HandleFunc("/logs", s.handleLogs)

	srv := &http.Server{Handler: mux}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// buildStatus assembles the current StatusResponse.
func (s *Server) buildStatus() StatusResponse {
	s.mu.Lock()
	vm, ctl, booting := s.vm, s.ctl, s.booting
	startedAt := s.startedAt
	s.mu.Unlock()

	resp := StatusResponse{State: "stopped", IdleIn: s.idleIn()}
	if booting {
		resp.State = "booting"
	}
	if vm != nil {
		resp.State = "running"
		resp.Uptime = time.Since(startedAt)
		if b, err := footprint.Read(vm.Pid()); err == nil {
			resp.VMFootprint = b
		}
	}
	peak := s.footHist.Peak()
	resp.VMFootprintPeak = FootprintPeak{Bytes: peak.Bytes, At: peak.At}
	if ctl != nil {
		resp.Guest = ctl.MemStats()
	}
	if logical, onDisk, err := diskimg.Usage(s.paths.DataImage()); err == nil {
		resp.Disk = DiskStatus{Logical: logical, OnDisk: onDisk}
	}
	if b, ok := dockerDesktopFootprint(); ok {
		resp.DockerDesktopFootprint = &b
	}
	return resp
}

// handleStatus implements GET /status for "barely status".
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.buildStatus())
}

// handleTop streams a TopEvent every interval (default 1s): the first
// event carries the full footprint history, later ones just the newest
// sample, plus a fresh container list each time.
func (s *Server) handleTop(w http.ResponseWriter, r *http.Request) {
	interval := time.Second
	if v := r.URL.Query().Get("interval"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			interval = d
		}
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	first := true
	for {
		ev := TopEvent{StatusResponse: s.buildStatus(), Containers: s.containerStats()}
		if first {
			ev.History = s.footHist.Samples()
			first = false
		}
		body, err := json.Marshal(ev)
		if err != nil {
			return
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", body); err != nil {
			return
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

// containerStats reports each running container's memory and CPU use,
// best-effort. Errors are swallowed: "top" should keep showing the host-side
// numbers even if the guest side is briefly unreachable.
func (s *Server) containerStats() []ContainerStat {
	conn, err := net.DialTimeout("unix", s.paths.V2375(), 2*time.Second)
	if err != nil {
		return nil
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))

	req, _ := http.NewRequest(http.MethodGet, "http://unix/containers/json?all=0", nil)
	if err := req.Write(conn); err != nil {
		return nil
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var list []struct {
		Names []string `json:"Names"`
		ID    string   `json:"Id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil
	}

	stats := make([]ContainerStat, 0, len(list))
	for _, c := range list {
		name := c.ID
		if len(c.Names) > 0 {
			name = c.Names[0]
		}
		mem, cpu, err := s.oneContainerStat(c.ID)
		if err != nil {
			continue
		}
		stats = append(stats, ContainerStat{Name: name, Mem: mem, CPU: cpu})
	}
	return stats
}

// oneContainerStat takes a single non-streaming stats sample for one
// container; the CPU percentage is derived from dockerd's pre-sample, so it
// covers the interval dockerd itself measured, not the top refresh interval.
func (s *Server) oneContainerStat(id string) (mem uint64, cpuPercent float64, err error) {
	conn, err := net.DialTimeout("unix", s.paths.V2375(), 2*time.Second)
	if err != nil {
		return 0, 0, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))

	req, _ := http.NewRequest(http.MethodGet, "http://unix/containers/"+id+"/stats?stream=false", nil)
	if err := req.Write(conn); err != nil {
		return 0, 0, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	var st dockerStats
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return 0, 0, err
	}
	cpuDelta := float64(st.CPUStats.CPUUsage.Total - st.PreCPUStats.CPUUsage.Total)
	sysDelta := float64(st.CPUStats.SystemUsage - st.PreCPUStats.SystemUsage)
	// dockerd fills percpu_usage only on cgroup v1; the guest runs cgroup v2,
	// where the CPU count comes as online_cpus.
	cpus := st.CPUStats.OnlineCPUs
	if cpus == 0 {
		cpus = len(st.CPUStats.CPUUsage.PerCPU)
	}
	if sysDelta > 0 && cpuDelta > 0 {
		cpuPercent = (cpuDelta / sysDelta) * float64(cpus) * 100
	}
	return st.MemoryStats.Usage, cpuPercent, nil
}

// dockerStats is the subset of dockerd's /containers/{id}/stats response
// used for "top"'s per-container row.
type dockerStats struct {
	MemoryStats struct {
		Usage uint64 `json:"usage"`
	} `json:"memory_stats"`
	CPUStats struct {
		CPUUsage struct {
			Total  uint64   `json:"total_usage"`
			PerCPU []uint64 `json:"percpu_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs  int    `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage struct {
			Total uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
}

// handleStop implements POST /stop: clean shutdown now. It answers once the
// VM is down, so "barely stop" returning means it has stopped.
// The host process exits once the response is flushed.
func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	// exiting first: clients that reach ensureVM during or after the
	// shutdown get errStopping instead of booting a VM into a process that
	// is about to exit.
	s.mu.Lock()
	s.exiting = true
	s.mu.Unlock()
	s.shutdown()
	writeJSON(w, struct{}{})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	// After the flush: requestExit ends Run and the process, which would
	// otherwise race the reply.
	s.requestExit()
}

// handleRestart implements POST /restart: clean shutdown, then boot again
// with the config reloaded from disk.
func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	// Not marked exiting: the process stays up and boots again below.
	s.shutdown()
	if err := s.reloadConfig(); err != nil {
		writeJSONError(w, err)
		return
	}
	if _, err := s.ensureVM(r.Context()); err != nil {
		writeJSONError(w, err)
		return
	}
	writeJSON(w, struct{}{})
}

// handleShell implements POST /shell: upgrades the connection to a raw
// stream carrying the session, same shape as Docker's own hijack.
// The session id is returned in the
// X-Barely-Session response header before the upgrade, so the CLI can later
// call /shell/{id}/resize; the exit code is retrieved from
// /shell/{id}/exit once the stream closes, since a hijacked connection has
// no in-band framing left to carry it.
func (s *Server) handleShell(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Argv []string `json:"argv"`
		TTY  bool     `json:"tty"`
		Rows int      `json:"rows"`
		Cols int      `json:"cols"`
		Env  []string `json:"env"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Argv) == 0 {
		req.Argv = []string{"/bin/sh", "-l"}
	}

	ctl, err := s.ensureVM(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	session, exitCh := ctl.NewSession()
	s.registerShellExit(session, exitCh)
	s.shellSessions.Add(1)
	defer s.shellSessions.Add(-1)

	log.Printf("serve: handleShell: session %d argv %v tty %v", session, req.Argv, req.TTY)
	if err := ctl.ShellOpen(session, req.Argv, req.TTY, req.Rows, req.Cols, req.Env, 30*time.Second); err != nil {
		log.Printf("serve: handleShell: ShellOpen: %v", err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	log.Printf("serve: handleShell: session %d ShellOpen ok, dialing v1025", session)

	guestConn, err := net.DialTimeout("unix", s.paths.V1025(), 30*time.Second)
	if err != nil {
		log.Printf("serve: handleShell: dial v1025: %v", err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer guestConn.Close()
	if err := proto.WriteSessionHeader(guestConn, uint32(session)); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	log.Printf("serve: handleShell: session %d header written, hijacking", session)

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	clientConn, buf, err := hj.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()
	fmt.Fprintf(clientConn, "HTTP/1.1 200 OK\r\nX-Barely-Session: %d\r\n\r\n", session)

	// Propagate half-close in both directions, same as dockerproxy.Splice:
	// a one-off command's client has nothing to send and its stdin EOFs
	// immediately, which must reach the agent as EOF too rather than
	// leaving both copies blocked forever.
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(guestConn, buf)
		halfCloseWrite(guestConn)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(clientConn, guestConn)
		halfCloseWrite(clientConn)
		done <- struct{}{}
	}()
	<-done
	<-done
}

func halfCloseWrite(conn net.Conn) {
	hc, ok := conn.(interface{ CloseWrite() error })
	log.Printf("serve: halfCloseWrite: type %T ok=%v", conn, ok)
	if ok {
		err := hc.CloseWrite()
		log.Printf("serve: halfCloseWrite: CloseWrite err=%v", err)
	}
}

// handleShellResize implements POST /shell/{id}/resize and GET
// /shell/{id}/exit.
func (s *Server) handleShellResize(w http.ResponseWriter, r *http.Request) {
	var session int
	var action string
	if _, err := fmt.Sscanf(r.URL.Path, "/shell/%d/%s", &session, &action); err != nil {
		http.NotFound(w, r)
		return
	}
	switch action {
	case "resize":
		var req struct{ Rows, Cols int }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		ctl := s.ctl
		s.mu.Unlock()
		if ctl == nil {
			http.Error(w, "VM not running", http.StatusServiceUnavailable)
			return
		}
		if err := ctl.ShellResize(session, req.Rows, req.Cols, 5*time.Second); err != nil {
			writeJSONError(w, err)
			return
		}
		writeJSON(w, struct{}{})
	case "exit":
		code, ok := s.waitShellExit(session, 24*time.Hour)
		if !ok {
			http.Error(w, "unknown session", http.StatusNotFound)
			return
		}
		writeJSON(w, struct{ Code int }{Code: code})
	default:
		http.NotFound(w, r)
	}
}

// handleLogs implements GET /logs?source=serve|vm|console&follow=bool by
// streaming the log file. Nothing in this repo calls it: "barely logs" reads
// the same files directly, so that it works when the host process isn't
// running.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	path := s.logPath(r.URL.Query().Get("source"))
	if path == "" {
		http.Error(w, "unknown source", http.StatusBadRequest)
		return
	}
	follow := r.URL.Query().Get("follow") == "true"
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	flusher, _ := w.(http.Flusher)
	io.Copy(w, f)
	if flusher != nil {
		flusher.Flush()
	}
	if !follow {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(500 * time.Millisecond):
			io.Copy(w, f)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func (s *Server) logPath(source string) string {
	switch source {
	case "serve":
		return s.paths.ServeLog()
	case "vm":
		return s.paths.VMLog()
	case "console":
		return s.paths.ConsoleLog()
	default:
		return ""
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{Error: err.Error()})
}
