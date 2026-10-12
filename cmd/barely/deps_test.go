package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestDanglingPluginsAndCredentialHelper pins the Docker Desktop leftovers
// doctor and setup warn about, and that a missing docker CLI ends checkDeps
// early. Needs no docker install: PATH is stubbed out.
func TestDanglingPluginsAndCredentialHelper(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dir)
	t.Setenv("PATH", "/usr/bin:/bin")
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"credsStore":"desktop","auths":{"x":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cli-plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "cli-plugins", "docker-buildx")
	if err := os.Symlink("/nonexistent/docker-buildx", link); err != nil {
		t.Fatal(err)
	}

	if got := danglingPlugins(); len(got) != 1 || got[0] != link {
		t.Errorf("danglingPlugins = %v, want [%s]", got, link)
	}

	// With no docker on PATH the check stops at the one fatal problem.
	deps := checkDeps()
	if len(deps) != 1 || !deps[0].fatal || deps[0].ok {
		t.Errorf("checkDeps without docker = %+v, want one fatal dep", deps)
	}
}

// TestEnsureBrewPluginDirKeepsOtherKeys protects the user's docker
// config.json: setup edits it in place, so credsStore and currentContext must
// survive and a second run must add nothing. Skips without Homebrew's plugin
// dir.
func TestEnsureBrewPluginDirKeepsOtherKeys(t *testing.T) {
	if !fileExists(brewPluginDir) {
		t.Skip("no Homebrew plugin dir on this machine")
	}
	dir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"credsStore":"osxkeychain","currentContext":"barely"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for i, want := range []bool{true, false} {
		added, err := ensureBrewPluginDir()
		if err != nil || added != want {
			t.Fatalf("call %d: added=%v err=%v, want added=%v", i+1, added, err, want)
		}
	}
	var cfg map[string]any
	b, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["credsStore"] != "osxkeychain" || cfg["currentContext"] != "barely" {
		t.Errorf("other keys changed: %v", cfg)
	}
	if dirs, _ := cfg["cliPluginsExtraDirs"].([]any); len(dirs) != 1 || dirs[0] != brewPluginDir {
		t.Errorf("cliPluginsExtraDirs = %v", cfg["cliPluginsExtraDirs"])
	}
}
