// Package state locates barely's state directory (~/.barely) and the fixed paths
// inside it.
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

type Paths struct {
	Root string
}

func New(root string) Paths { return Paths{Root: root} }

func (p Paths) Config() string      { return filepath.Join(p.Root, "config.toml") }
func (p Paths) BinDir() string      { return filepath.Join(p.Root, "bin") }
func (p Paths) DockerSock() string  { return filepath.Join(p.Root, "docker.sock") }
func (p Paths) ControlSock() string { return filepath.Join(p.Root, "control.sock") }
func (p Paths) GuestDir() string    { return filepath.Join(p.Root, "guest") }
func (p Paths) DataImage() string   { return filepath.Join(p.Root, "data.img") }
func (p Paths) LogsDir() string     { return filepath.Join(p.Root, "logs") }
func (p Paths) ServeLog() string    { return filepath.Join(p.Root, "logs", "serve.log") }
func (p Paths) VMLog() string       { return filepath.Join(p.Root, "logs", "vm.log") }
func (p Paths) ConsoleLog() string  { return filepath.Join(p.Root, "logs", "console.log") }
func (p Paths) RunDir() string      { return filepath.Join(p.Root, "run") }
func (p Paths) ServeLock() string   { return filepath.Join(p.Root, "run", "serve.lock") }
func (p Paths) V1024() string       { return filepath.Join(p.Root, "run", "v1024") }
func (p Paths) V1025() string       { return filepath.Join(p.Root, "run", "v1025") }
func (p Paths) V2375() string       { return filepath.Join(p.Root, "run", "v2375") }
