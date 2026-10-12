// Package serve is the host process ("barely serve"): loads config, finds the
// guest image, boots and supervises the VM process, and proxies the
// Docker socket to it.
package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"barely/internal/config"
	"barely/internal/control"
	"barely/internal/diskimg"
	"barely/internal/dockerproxy"
	"barely/internal/footprint"
	"barely/internal/guestargs"
	"barely/internal/guestimg"
	"barely/internal/launchd"
	"barely/internal/lock"
	"barely/internal/netstack"
	"barely/internal/portforward"
	"barely/internal/proto"
	"barely/internal/state"
	"barely/internal/vmproc"
	"barely/internal/vmtypes"
)

const (
	controlDialTimeout  = 60 * time.Second
	helloAttemptTimeout = 5 * time.Second
	dockerDialTimeout   = 30 * time.Second
	shutdownGrace       = 30 * time.Second
	shutdownKill        = 10 * time.Second
)

// Server is the running host process.
type Server struct {
	barelyPath string
	paths      state.Paths
	net        *netstack.Network
	ports      *portforward.Watcher
	vmLog      *os.File
	footHist   *footprint.History
	lk         *lock.Lock

	// clients and shellSessions count open docker.sock connections and
	// open "barely shell" sessions respectively, for idle tracking.
	clients       atomic.Int64
	shellSessions atomic.Int64

	// bootMu serialises boots so a slow boot never holds mu, which status,
	// top and the idle loop need.
	bootMu sync.Mutex
	runCtx context.Context

	mu        sync.Mutex
	booting   bool
	exiting   bool // set by /stop; ensureVM boots nothing from then on
	cfg       vmtypes.Config
	vm        *vmproc.Process
	ctl       *control.Client
	crashes   []time.Time // unexpected VM process exits, for crash-loop detection
	startedAt time.Time
	idleSince time.Time
	cancel    context.CancelFunc

	shellExitsMu sync.Mutex
	shellExits   map[int]<-chan int
}

// errStopping answers a client that reached the host process while it was
// exiting. Its next attempt starts a fresh process.
var errStopping = errors.New("serve: barely is stopping; run the command again")

// Crash-loop guard: if the VM process exits unexpectedly this many times
// within the window, stop rebooting it on new client connections and
// surface a clear error instead of retrying forever.
const (
	crashLoopCount  = 3
	crashLoopWindow = time.Minute
)

// New loads config and prepares the host process. barelyPath is the path to
// this binary; the barelyvm helper is looked up next to it.
func New(barelyPath, stateDir string) (*Server, error) {
	paths := state.New(stateDir)
	cfg, err := config.Load(paths.Config())
	if err != nil {
		return nil, err
	}
	vmLog, err := os.OpenFile(paths.VMLog(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("serve: opening vm.log: %w", err)
	}
	nw, err := netstack.New()
	if err != nil {
		return nil, err
	}
	ports := portforward.New(paths.V2375(), netstack.DeviceIP, nw)
	return &Server{
		barelyPath: barelyPath,
		paths:      paths,
		cfg:        cfg,
		net:        nw,
		ports:      ports,
		vmLog:      vmLog,
		footHist:   footprint.NewHistory(3600), // ~1hr at 1s
		shellExits: map[int]<-chan int{},
	}, nil
}

// reloadConfig re-reads config.toml, for "barely restart".
func (s *Server) reloadConfig() error {
	cfg, err := config.Load(s.paths.Config())
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	return nil
}

// requestExit cancels the context Run is serving under, which it treats
// exactly like an external SIGTERM: stop accepting, shut the guest down,
// and return.
func (s *Server) requestExit() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// registerShellExit and waitShellExit hand a shell session's exit channel
// (from control.Client.NewSession) across the two separate control-API
// requests involved: POST /shell, which opens the session, and GET
// /shell/{id}/exit, which the CLI calls once the hijacked stream closes to
// retrieve the process's exit code.
func (s *Server) registerShellExit(session int, exit <-chan int) {
	s.shellExitsMu.Lock()
	s.shellExits[session] = exit
	s.shellExitsMu.Unlock()
}

func (s *Server) waitShellExit(session int, timeout time.Duration) (code int, ok bool) {
	s.shellExitsMu.Lock()
	exit, found := s.shellExits[session]
	delete(s.shellExits, session)
	s.shellExitsMu.Unlock()
	if !found {
		return 0, false
	}
	select {
	case c := <-exit:
		return c, true
	case <-time.After(timeout):
		return 0, false
	}
}

// Run takes the single-instance lock, listens on docker.sock (launchd's
// activated socket if it started this process that way, otherwise binding
// the path itself) and serves connections until ctx is cancelled — by the
// caller (e.g. SIGTERM) or by idleLoop/requestExit — at which point it
// shuts the guest down cleanly.
func (s *Server) Run(ctx context.Context) error {
	lk, err := lock.Acquire(s.paths.ServeLock())
	if err != nil {
		if err == lock.ErrHeld {
			log.Print("serve: another instance already holds the lock, exiting")
			return nil
		}
		return err
	}
	s.lk = lk
	defer lk.Release()

	ln, activated, err := launchd.ActivateSocket("docker")
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	sockPath := s.paths.DockerSock()
	if !activated {
		os.Remove(sockPath)
		ln, err = net.Listen("unix", sockPath)
		if err != nil {
			return fmt.Errorf("serve: listening on %s: %w", sockPath, err)
		}
		if err := os.Chmod(sockPath, 0o600); err != nil {
			ln.Close()
			return err
		}
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancel = cancel
	s.runCtx = ctx
	s.startedAt = time.Now()
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	go s.idleLoop(ctx, cancel)
	go s.wakeLoop(ctx)
	go func() {
		if err := s.startControlAPI(ctx); err != nil {
			log.Printf("serve: control API: %v", err)
		}
	}()

	log.Printf("serve: listening on %s (launchd socket activation: %v)", sockPath, activated)
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				s.shutdown()
				return nil
			default:
				return err
			}
		}
		go s.handleClient(ctx, conn)
	}
}

func (s *Server) handleClient(ctx context.Context, client net.Conn) {
	acceptedAt := time.Now()
	defer client.Close()
	s.clients.Add(1)
	defer s.clients.Add(-1)
	wasRunning := s.vmRunning()
	if _, err := s.ensureVM(ctx); err != nil {
		log.Print(err)
		writeDockerError(client, err)
		return
	}
	if !wasRunning {
		log.Printf("serve: cold start: +%s accept to VM ready for the docker proxy", time.Since(acceptedAt).Round(time.Millisecond))
	}
	if err := dockerproxy.Splice(client, s.paths.V2375(), dockerDialTimeout); err != nil {
		log.Printf("serve: %v", err)
	}
}

func (s *Server) vmRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.vm != nil
}

// writeDockerError answers the client's pending request with err in the
// Engine API's error shape, so the docker CLI prints "Error response from
// daemon: ..." instead of an unexplained EOF.
func writeDockerError(client net.Conn, err error) {
	body, _ := json.Marshal(struct {
		Message string `json:"message"`
	}{Message: "barely: " + err.Error()})
	fmt.Fprintf(client, "HTTP/1.1 503 Service Unavailable\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
}

// ensureVM boots the VM process if one isn't already running and returns
// the connected control-channel client.
func (s *Server) ensureVM(ctx context.Context) (*control.Client, error) {
	s.bootMu.Lock()
	defer s.bootMu.Unlock()

	s.mu.Lock()
	// A client that connected while the VM was shutting down has been waiting
	// on bootMu. Booting for it now would start a VM in a process that is
	// about to exit and take that VM down with it.
	if s.exiting || (s.runCtx != nil && s.runCtx.Err() != nil) {
		s.mu.Unlock()
		return nil, errStopping
	}
	if s.vm != nil {
		select {
		case <-s.vm.Done():
			// Exited since we last checked; fall through and reboot.
			s.vm, s.ctl = nil, nil
		default:
			ctl := s.ctl
			s.mu.Unlock()
			return ctl, nil
		}
	}
	if n := s.recentCrashes(); n >= crashLoopCount {
		s.mu.Unlock()
		return nil, fmt.Errorf("serve: VM process crashed %d times in the last %s, not retrying automatically; see %s", n, crashLoopWindow, s.paths.ConsoleLog())
	}
	s.booting = true
	runCtx := s.runCtx
	s.mu.Unlock()

	vm, ctl, err := s.boot()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.booting = false
	if err != nil {
		return nil, err
	}
	s.vm, s.ctl = vm, ctl
	if runCtx == nil {
		runCtx = context.Background()
	}
	portsCtx, stopPorts := context.WithCancel(runCtx)
	go s.ports.Run(portsCtx)
	go s.trimLoop(portsCtx, ctl)
	go s.fsEventsLoop(portsCtx, ctl, append([]string(nil), s.cfg.Shares...))
	go s.watch(vm, stopPorts)
	return ctl, nil
}

func (s *Server) boot() (*vmproc.Process, *control.Client, error) {
	bootStart := time.Now()
	helper, err := FindHelper(s.barelyPath, s.paths.BinDir())
	if err != nil {
		return nil, nil, fmt.Errorf("serve: %w", err)
	}
	if err := checkHypervisorEntitlement(helper); err != nil {
		return nil, nil, fmt.Errorf("serve: %w", err)
	}
	img, err := guestimg.Find(s.paths.GuestDir(), proto.Version)
	if err != nil {
		return nil, nil, fmt.Errorf("serve: %w", err)
	}
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()
	cpus := config.EffectiveCPUs(cfg)

	shares := make([]vmtypes.Share, len(cfg.Shares))
	for i, p := range cfg.Shares {
		shares[i] = vmtypes.Share{HostPath: p, Tag: fmt.Sprintf("share%d", i)}
	}
	args := guestargs.Args{Shares: shares, DockerSockAliases: []string{s.paths.DockerSock()}}

	if err := os.MkdirAll(s.paths.RunDir(), 0o700); err != nil {
		return nil, nil, err
	}
	for _, p := range []string{s.paths.V1024(), s.paths.V1025(), s.paths.V2375()} {
		os.Remove(p)
	}
	for _, p := range []string{s.paths.V1024(), s.paths.V1025(), s.paths.V2375()} {
		if len(p) > 103 {
			return nil, nil, fmt.Errorf("serve: socket path %q exceeds the 104-byte unix socket limit", p)
		}
	}

	if err := diskimg.EnsureSize(s.paths.DataImage(), uint64(cfg.DiskMax)); err != nil {
		return nil, nil, fmt.Errorf("serve: %w", err)
	}
	log.Printf("serve: boot: +%s data.img ready", time.Since(bootStart).Round(time.Millisecond))

	spec := vmtypes.VMSpec{
		CPUs:        cpus,
		MemoryBytes: uint64(cfg.MemoryMax),
		Kernel:      img.Kernel(),
		Initrd:      img.Initrd(),
		Cmdline:     "console=hvc0 swiotlb=512 cma=0 page_reporting.page_reporting_order=4 virtiofs.default_permissions=0 " + args.Encode(),
		RootImage:   img.RootImage(),
		DataImage:   s.paths.DataImage(),
		DataCap:     uint64(cfg.DiskMax),
		Shares:      shares,
		Vsock: []vmtypes.VsockPort{
			{Port: proto.PortControl, Path: s.paths.V1024(), Listen: true},
			{Port: proto.PortShell, Path: s.paths.V1025(), Listen: true},
			{Port: proto.PortDocker, Path: s.paths.V2375(), Listen: true},
		},
		ConsoleLog: s.paths.ConsoleLog(),
	}

	ctx := context.Background()
	netFD, closeGateway, err := s.net.Socketpair(ctx)
	if err != nil {
		return nil, nil, err
	}

	ok := false
	defer func() {
		if !ok {
			closeGateway()
		}
	}()

	vm, err := vmproc.Start(helper, spec, netFD, s.vmLog)
	if err != nil {
		return nil, nil, err
	}
	log.Printf("serve: boot: +%s VM process started", time.Since(bootStart).Round(time.Millisecond))

	type helloResult struct {
		ctl *control.Client
		err error
	}
	helloCh := make(chan helloResult, 1)
	go func() {
		ctl, _, err := control.DialAndHello(s.paths.V1024(), controlDialTimeout, helloAttemptTimeout)
		helloCh <- helloResult{ctl: ctl, err: err}
	}()

	// libkrun aborts the whole VM process when a device fails to
	// activate (e.g. an unreachable network backend), so don't sit out
	// the full hello timeout once it's gone.
	select {
	case r := <-helloCh:
		if r.err != nil {
			vm.Stop(shutdownGrace, shutdownKill)
			return nil, nil, fmt.Errorf("serve: %w", r.err)
		}
		ok = true
		log.Printf("serve: boot: +%s hello answered (kernel boot + agent startup)", time.Since(bootStart).Round(time.Millisecond))
		go s.logDockerdReady(r.ctl, bootStart)
		s.footHist.Reset()
		go s.sampleFootprint(vm, vm.Done())
		return vm, r.ctl, nil
	case <-vm.Done():
		go func() {
			if r := <-helloCh; r.ctl != nil {
				r.ctl.Close()
			}
		}()
		exitErr, _ := vm.Wait()
		return nil, nil, fmt.Errorf("serve: VM process exited during boot (%v); last lines of %s:\n%s", exitErr, s.paths.VMLog(), tailLines(s.paths.VMLog(), 20))
	}
}

// logDockerdReady polls ctl.Ready() (set by the agent's unsolicited
// "ready" once dockerd answers) to log how much of a boot's total time is
// the guest side: overlay/data-disk mount, network, and dockerd/containerd
// startup — the dominant cost.
func (s *Server) logDockerdReady(ctl *control.Client, bootStart time.Time) {
	for i := 0; i < 600; i++ {
		if ctl.Ready() {
			log.Printf("serve: boot: +%s dockerd ready", time.Since(bootStart).Round(time.Millisecond))
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// tailLines returns up to the last n lines of path, for putting the VM
// process's own last words into an error.
func tailLines(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return err.Error()
	}
	defer f.Close()
	const maxTail = 16 << 10
	if fi, err := f.Stat(); err == nil && fi.Size() > maxTail {
		f.Seek(-maxTail, io.SeekEnd)
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return err.Error()
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// watch logs an unexpected VM process exit and clears it so the next
// client connection boots a new one.
func (s *Server) watch(vm *vmproc.Process, stopPorts context.CancelFunc) {
	err, requested := vm.Wait()
	stopPorts()
	s.mu.Lock()
	if s.vm == vm {
		s.vm, s.ctl = nil, nil
	}
	s.mu.Unlock()
	if !requested {
		log.Printf("serve: VM process exited unexpectedly: %v (see %s); last lines of %s:\n%s", err, s.paths.ConsoleLog(), s.paths.VMLog(), tailLines(s.paths.VMLog(), 20))
		s.mu.Lock()
		s.crashes = append(s.crashes, time.Now())
		s.mu.Unlock()
	}
}

// recentCrashes returns the number of unexpected exits within
// crashLoopWindow, pruning older ones. Must be called with s.mu held.
func (s *Server) recentCrashes() int {
	cutoff := time.Now().Add(-crashLoopWindow)
	kept := s.crashes[:0]
	for _, t := range s.crashes {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	s.crashes = kept
	return len(s.crashes)
}

func (s *Server) shutdown() {
	// Held until the VM process has exited: a client connecting mid-shutdown
	// would otherwise boot a second VM on data.img while this one is still
	// trimming it, and two guests writing one ext4 corrupt it.
	s.bootMu.Lock()
	defer s.bootMu.Unlock()
	s.mu.Lock()
	vm, ctl := s.vm, s.ctl
	s.vm, s.ctl = nil, nil
	s.mu.Unlock()
	if vm == nil {
		return
	}
	if ctl != nil {
		s.fstrim(ctl, "before shutdown")
		ctl.Shutdown(shutdownGrace)
		ctl.Close()
	}
	vm.Stop(shutdownGrace, shutdownKill)
}
