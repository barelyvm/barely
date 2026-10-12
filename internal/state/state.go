// Package state locates barely's state directory (~/.barely) and the fixed paths
// inside it, so the CLI, the host process and the launchd plist agree on where
// sockets, logs, the data disk and the guest image live.
//
// Used by: cmd/barely and internal/serve.
package state

import (
	"os"
	"path/filepath"
)

// Dir returns ~/.barely without creating it; callers that need the layout to
// exist call EnsureDirs. BARELY_HOME overrides it so several instances (e.g.
// parallel development VMs) can run without sharing sockets or disks.
func Dir() (string, error) {
	if dir := os.Getenv("BARELY_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".barely"), nil
}

// DefaultDir returns ~/.barely regardless of BARELY_HOME, for comparing against
// Dir() to tell a development state directory apart from the real one
// (e.g. internal/launchd.LabelForStateDir).
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".barely"), nil
}

// EnsureDirs creates dir and its run/, logs/, guest/
// subdirectories if they don't exist yet.
func EnsureDirs(dir string) error {
	for _, sub := range []string{"", "run", "logs", "guest"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return err
		}
	}
	return nil
}

// Paths names the files under one state directory. The vsock sockets under
// run/ (v1024 control, v1025 shell, v2375 docker) are the host ends of the
// guest's vsock ports; unix socket paths are limited to 104 bytes, so the
// names stay short.
type Paths struct {
	Root string
}

func New(root string) Paths { return Paths{Root: root} }

func (p Paths) Config() string { return filepath.Join(p.Root, "config.toml") }
func (p Paths) BinDir() string { return filepath.Join(p.Root, "bin") }

// DockerSock is the socket Docker clients connect to; launchd owns it and
// starts the host process on the first connection.
func (p Paths) DockerSock() string { return filepath.Join(p.Root, "docker.sock") }

// ControlSock is the host process's HTTP control API, used by the barely CLI
// (status, shell, restart, logs). Not a vsock end; the guest's control port is V1024.
func (p Paths) ControlSock() string { return filepath.Join(p.Root, "control.sock") }

func (p Paths) GuestDir() string { return filepath.Join(p.Root, "guest") }

// DataImage is data.img, the sparse disk holding images, containers and volumes.
func (p Paths) DataImage() string { return filepath.Join(p.Root, "data.img") }
func (p Paths) LogsDir() string   { return filepath.Join(p.Root, "logs") }

// ServeLog is the host process's log, also the launchd agent's stderr.
func (p Paths) ServeLog() string { return filepath.Join(p.Root, "logs", "serve.log") }

// VMLog is the VM process's stdout and stderr, as captured by the host process.
func (p Paths) VMLog() string { return filepath.Join(p.Root, "logs", "vm.log") }

// ConsoleLog is the guest's kernel and init console output.
func (p Paths) ConsoleLog() string { return filepath.Join(p.Root, "logs", "console.log") }
func (p Paths) RunDir() string     { return filepath.Join(p.Root, "run") }

// ServeLock is the file internal/lock flocks to keep one host process per state directory.
func (p Paths) ServeLock() string { return filepath.Join(p.Root, "run", "serve.lock") }
func (p Paths) V1024() string     { return filepath.Join(p.Root, "run", "v1024") }
func (p Paths) V1025() string     { return filepath.Join(p.Root, "run", "v1025") }
func (p Paths) V2375() string     { return filepath.Join(p.Root, "run", "v2375") }
