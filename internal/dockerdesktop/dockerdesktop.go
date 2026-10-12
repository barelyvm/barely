// Package dockerdesktop detects whether Docker Desktop's VM is running:
// barely status/top/doctor
// all report it alongside barely's own VM.
package dockerdesktop

import (
	"os/exec"
	"strconv"
	"strings"

	"barely/internal/footprint"
)

// PID returns Docker Desktop's VM backend process's pid ("com.docker.backend",
// its Apple Silicon hypervisor process) and true, or (0, false) if it isn't
// running.
func PID() (int, bool) {
	out, err := exec.Command("pgrep", "-x", "com.docker.backend").Output()
	if err != nil {
		return 0, false
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	pid, err := strconv.Atoi(line)
	if err != nil {
		return 0, false
	}
	return pid, true
}

// Footprint returns Docker Desktop's VM footprint and true when it's
// running, or (0, false) when it isn't.
func Footprint() (uint64, bool) {
	pid, ok := PID()
	if !ok {
		return 0, false
	}
	b, err := footprint.Read(pid)
	if err != nil {
		return 0, false
	}
	return b, true
}
