package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The shell line pins docker to barely's socket through DOCKER_HOST, which
// overrides the Docker context. Nothing else on the machine (Docker
// Desktop launching, another tool running `docker context use`) can then
// move a terminal's docker commands off barely.
const (
	shellBlockStart = "# >>> barely >>>"
	shellBlockEnd   = "# <<< barely <<<"
)

// shellProfile is where the line goes for the user's login shell.
type shellProfile struct {
	shell string
	path  string
	// own means the whole file is ours (fish's conf.d), so uninstall
	// deletes it instead of cutting a block out.
	own  bool
	line string
}

// detectShellProfile picks the file setup and uninstall edit from $SHELL. bash
// gets .bash_profile because Terminal.app starts bash as a login shell, which
// never reads .bashrc.
func detectShellProfile(dockerSock string) (shellProfile, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return shellProfile{}, err
	}
	export := fmt.Sprintf("export DOCKER_HOST=unix://%s", dockerSock)
	switch shell := filepath.Base(os.Getenv("SHELL")); shell {
	case "zsh":
		return shellProfile{shell: shell, path: filepath.Join(home, ".zshrc"), line: export}, nil
	case "bash":
		return shellProfile{shell: shell, path: filepath.Join(home, ".bash_profile"), line: export}, nil
	case "fish":
		return shellProfile{
			shell: shell,
			path:  filepath.Join(home, ".config", "fish", "conf.d", "barely.fish"),
			own:   true,
			line:  fmt.Sprintf("set -gx DOCKER_HOST unix://%s", dockerSock),
		}, nil
	default:
		return shellProfile{}, fmt.Errorf("don't know where %q keeps its profile; add this yourself: %s", shell, export)
	}
}

func (p shellProfile) block() string {
	return shellBlockStart + "\n" + p.line + "\n" + shellBlockEnd + "\n"
}

// install writes the line, replacing an earlier barely block if there is one.
func (p shellProfile) install() error {
	if p.own {
		if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p.path, []byte(p.block()), 0o644)
	}
	b, err := os.ReadFile(p.path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	rest := stripShellBlock(string(b))
	if rest != "" && !strings.HasSuffix(rest, "\n") {
		rest += "\n"
	}
	return os.WriteFile(p.path, []byte(rest+p.block()), 0o644)
}

// remove takes the line back out; a profile without it is left alone.
func (p shellProfile) remove() (removed bool, err error) {
	b, err := os.ReadFile(p.path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !strings.Contains(string(b), shellBlockStart) {
		return false, nil
	}
	if p.own {
		return true, os.Remove(p.path)
	}
	return true, os.WriteFile(p.path, []byte(stripShellBlock(string(b))), 0o644)
}

func stripShellBlock(s string) string {
	start := strings.Index(s, shellBlockStart)
	if start < 0 {
		return s
	}
	end := strings.Index(s[start:], shellBlockEnd)
	if end < 0 {
		return s
	}
	tail := strings.TrimPrefix(s[start+end+len(shellBlockEnd):], "\n")
	return s[:start] + tail
}
