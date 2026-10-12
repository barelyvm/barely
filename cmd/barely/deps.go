package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// brewPluginDir is where Homebrew's docker-compose and docker-buildx
// formulae put their CLI plugins. docker only looks there if config.json
// lists it under cliPluginsExtraDirs.
const brewPluginDir = "/opt/homebrew/lib/docker/cli-plugins"

const (
	// dockerDesktop's presence is a leftover that can take docker back from
	// barely; checkDeps warns and setup offers the DOCKER_HOST pin.
	dockerDesktop = "/Applications/Docker.app"
)

// dep is one thing barely needs on the Mac side, or a leftover that will trip
// the user up. Docker Desktop used to supply most of these, so a machine
// without it (or after uninstalling it) needs them from Homebrew.
type dep struct {
	name string
	ok   bool
	// detail is the version or location when ok, the problem when not.
	detail string
	// fix is the command or step that resolves it.
	fix string
	// fatal means barely can't be set up at all without it.
	fatal bool
}

func dockerConfigPath() string {
	if dir := os.Getenv("DOCKER_CONFIG"); dir != "" {
		return filepath.Join(dir, "config.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".docker", "config.json")
}

func readDockerConfig() map[string]any {
	cfg := map[string]any{}
	b, err := os.ReadFile(dockerConfigPath())
	if err != nil {
		return cfg
	}
	json.Unmarshal(b, &cfg)
	return cfg
}

// checkDeps reports the docker CLI, its compose and buildx plugins, the
// credential helpers config.json names, and Docker Desktop
// leftovers.
func checkDeps() []dep {
	var deps []dep

	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		// Without the CLI the plugin and credential checks below would only
		// repeat the same problem.
		deps = append(deps, dep{name: "docker CLI", detail: "not found on PATH", fix: "brew install docker", fatal: true})
		return deps
	}
	deps = append(deps, dep{name: "docker CLI", ok: true, detail: dockerPath})

	for _, p := range []struct{ name, formula string }{{"compose", "docker-compose"}, {"buildx", "docker-buildx"}} {
		out, err := exec.Command("docker", p.name, "version").Output()
		d := dep{name: "docker " + p.name}
		switch {
		case err == nil:
			d.ok, d.detail = true, strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		case fileExists(filepath.Join(brewPluginDir, "docker-"+p.name)):
			d.detail = "installed by Homebrew, not in docker's plugin dirs"
			d.fix = "barely setup"
		default:
			d.detail = "not installed"
			d.fix = "brew install " + p.formula
			if p.name == "buildx" {
				d.detail += "; builds use the legacy builder"
			}
		}
		deps = append(deps, d)
	}

	cfg := readDockerConfig()
	helpers := map[string]bool{}
	if s, ok := cfg["credsStore"].(string); ok && s != "" {
		helpers[s] = true
	}
	if m, ok := cfg["credHelpers"].(map[string]any); ok {
		for _, v := range m {
			if s, ok := v.(string); ok && s != "" {
				helpers[s] = true
			}
		}
	}
	names := make([]string, 0, len(helpers))
	for h := range helpers {
		names = append(names, h)
	}
	sort.Strings(names)
	for _, h := range names {
		bin := "docker-credential-" + h
		d := dep{name: "creds " + h}
		if p, err := exec.LookPath(bin); err == nil && fileExists(p) {
			d.ok, d.detail = true, p
		} else {
			d.detail = fmt.Sprintf("%s not installed; docker pull and login will fail", bin)
			switch h {
			case "osxkeychain":
				d.fix = "brew install docker-credential-helper"
			case "desktop":
				d.fix = fmt.Sprintf("brew install docker-credential-helper && set \"credsStore\": \"osxkeychain\" in %s", dockerConfigPath())
			default:
				d.fix = fmt.Sprintf("install %s, or remove it from %s", bin, dockerConfigPath())
			}
		}
		deps = append(deps, d)
	}

	if dangling := danglingPlugins(); len(dangling) > 0 {
		deps = append(deps, dep{
			name:   "plugin links",
			detail: "dangling: " + strings.Join(dangling, ", "),
			fix:    "rm " + strings.Join(dangling, " "),
		})
	}

	if fileExists(dockerDesktop) {
		deps = append(deps, dep{
			name:   "Docker Desktop",
			detail: "installed; launching it switches docker back to its own engine",
			fix:    "uninstall it, or barely setup -shell-env",
		})
	}
	return deps
}

// logLine prints one aligned status line: status, what, detail.
func logLine(status, name, detail string) {
	fmt.Printf("%-5s %-18s %s\n", status, name, detail)
}

// printDeps shows checkDeps' result for doctor and setup; the fatal return is
// what makes setup stop before it installs anything.
func printDeps(deps []dep) (fatal bool) {
	for _, d := range deps {
		status := "ok"
		if !d.ok {
			status = "warn"
			if d.fatal {
				status, fatal = "need", true
			}
		}
		logLine(status, d.name, d.detail)
		if d.fix != "" {
			logLine("", "", "fix: "+d.fix)
		}
	}
	return fatal
}

// danglingPlugins lists symlinks in ~/.docker/cli-plugins whose targets are
// gone, which is what uninstalling Docker Desktop leaves behind.
func danglingPlugins() []string {
	dir := filepath.Join(filepath.Dir(dockerConfigPath()), "cli-plugins")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if _, err := os.Stat(p); err != nil {
			out = append(out, p)
		}
	}
	return out
}

// ensureBrewPluginDir adds Homebrew's plugin folder to config.json's
// cliPluginsExtraDirs when Homebrew plugins exist but docker isn't looking
// there, so `brew install docker-compose docker-buildx` works without the
// manual step from the formula's caveats. Other keys are left as they are.
func ensureBrewPluginDir() (added bool, err error) {
	if !fileExists(brewPluginDir) {
		return false, nil
	}
	cfg := readDockerConfig()
	dirs, _ := cfg["cliPluginsExtraDirs"].([]any)
	for _, d := range dirs {
		if d == brewPluginDir {
			return false, nil
		}
	}
	cfg["cliPluginsExtraDirs"] = append(dirs, brewPluginDir)
	b, err := json.MarshalIndent(cfg, "", "\t")
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(dockerConfigPath()), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(dockerConfigPath(), append(b, '\n'), 0o600)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
