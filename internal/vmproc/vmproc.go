// Package vmproc starts and supervises the VM process ("barely vm").
// libkrun's krun_start_enter takes over the
// process that calls it and exit()s it when the guest shuts down, so the
// VM runs in a child the host process supervises rather than inside the
// host process itself.
package vmproc

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"barely/internal/vmtypes"
)

// Process is one running (or exited) VM process.
type Process struct {
	cmd       *exec.Cmd
	netFD     int // the VM process's end of the net socketpair, passed as fd 3
	deathW    *os.File
	exitErr   error
	done      chan struct{}
	stopOnce  sync.Once
	requested atomic.Bool // true once Stop asked for shutdown; suppresses "unexpected exit" logging
}

// Start execs helper (barelyvm) as "barelyvm run", handing it spec on stdin, netFD as fd 3
// and the read end of a fresh parent-death pipe as fd 4. logw receives the
// child's stdout/stderr (vm.log).
func Start(helper string, spec vmtypes.VMSpec, netFD int, logw *os.File) (*Process, error) {
	deathR, deathW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("vmproc: parent-death pipe: %w", err)
	}

	cmd := exec.Command(helper, "run")
	netFile := os.NewFile(uintptr(netFD), "vm-net")
	cmd.ExtraFiles = []*os.File{netFile, deathR} // fd 3, fd 4
	cmd.Stdout = logw
	cmd.Stderr = logw

	stdin, err := cmd.StdinPipe()
	if err != nil {
		deathR.Close()
		deathW.Close()
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		deathR.Close()
		deathW.Close()
		return nil, fmt.Errorf("vmproc: starting %s run: %w", helper, err)
	}
	// The child has its own copies of these fds now.
	netFile.Close()
	deathR.Close()

	if err := json.NewEncoder(stdin).Encode(spec); err != nil {
		cmd.Process.Kill()
		deathW.Close()
		return nil, fmt.Errorf("vmproc: writing VMSpec: %w", err)
	}
	stdin.Close()

	p := &Process{cmd: cmd, netFD: netFD, deathW: deathW, done: make(chan struct{})}
	go func() {
		p.exitErr = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

// Pid returns the VM process's pid, for status's proc_pid_rusage read of
// its memory footprint.
func (p *Process) Pid() int { return p.cmd.Process.Pid }

// Done is closed when the process has exited.
func (p *Process) Done() <-chan struct{} { return p.done }

// Wait blocks until the process exits and reports whether the exit was
// requested (via Stop) or unexpected.
func (p *Process) Wait() (err error, requested bool) {
	<-p.done
	return p.exitErr, p.requested.Load()
}

// Stop performs the shutdown sequence: the caller should
// already have asked the agent to shut down the guest over the control
// channel. Stop closes the parent-death pipe (in case the agent request
// didn't land) and, if the process hasn't exited within gracePeriod, sends
// SIGTERM then, after killPeriod, SIGKILL.
func (p *Process) Stop(gracePeriod, killPeriod time.Duration) {
	p.stopOnce.Do(func() {
		p.requested.Store(true)
		p.deathW.Close()
	})
	select {
	case <-p.done:
		return
	case <-time.After(gracePeriod):
	}
	p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.done:
		return
	case <-time.After(killPeriod):
	}
	p.cmd.Process.Kill()
	<-p.done
}
