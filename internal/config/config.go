// Package config loads barely's user config (~/.barely/config.toml: memory
// and disk caps, CPUs, idle timeout, shares, start-at-login), applying the
// defaults. The VM's resource limits are decided here, not in the helper: the
// host process loads the config and passes the result to the VM process.
//
// Entry points are Load, Default, Validate and EffectiveCPUs.
//
// Used by: cmd/barely (setup, config) and internal/serve (loaded at start and
// on restart).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/pelletier/go-toml/v2"

	"barely/internal/vmtypes"
)

// Default returns the config defaults: half the host's RAM, all cores,
// 100 GiB disk cap, 5 minute idle timeout, Docker Desktop's share set.
func Default() vmtypes.Config {
	return vmtypes.Config{
		MemoryMax:    vmtypes.ByteSize(defaultMemoryMax()),
		CPUs:         0,
		DiskMax:      100 << 30,
		IdleTimeout:  vmtypes.Duration(5 * time.Minute),
		Shares:       []string{"/Users", "/Volumes", "/private", "/tmp", "/var/folders"},
		StartAtLogin: false,
	}
}

func defaultMemoryMax() uint64 {
	// Half of hw.memsize; 4 GiB if the sysctl fails.
	out, err := sysctlPhysMem()
	if err != nil || out == 0 {
		return 4 << 30
	}
	return out / 2
}

// Load reads path, filling in defaults for anything unset, and validates
// the result. A missing file returns the defaults.
func Load(path string) (vmtypes.Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, Validate(cfg)
		}
		return vmtypes.Config{}, err
	}
	// Decode onto the defaults so unset keys keep them. Any key present
	// overwrites outright, including cpus = 0 ("all cores"), shares = []
	// ("no shares") and start_at_login = false, which are legitimate explicit
	// settings.
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return vmtypes.Config{}, fmt.Errorf("config: %s: %w", path, err)
	}
	if err := Validate(cfg); err != nil {
		return vmtypes.Config{}, fmt.Errorf("config: %s: %w", path, err)
	}
	return cfg, nil
}

// Validate rejects a config the VM can't honor. Load runs it on every read, so
// a bad hand edit fails loudly instead of at boot, and setup runs it before
// writing new values.
func Validate(cfg vmtypes.Config) error {
	if cfg.MemoryMax < 1<<30 {
		return fmt.Errorf("memory_max must be at least 1 GiB, got %s", cfg.MemoryMax)
	}
	if cfg.CPUs < 0 || cfg.CPUs > runtime.NumCPU() {
		return fmt.Errorf("cpus must be between 0 (all cores) and %d, got %d", runtime.NumCPU(), cfg.CPUs)
	}
	seen := map[string]bool{}
	for _, s := range cfg.Shares {
		if !filepath.IsAbs(s) {
			return fmt.Errorf("share %q must be an absolute path", s)
		}
		info, err := os.Stat(s)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("share %q does not exist or is not a directory", s)
		}
		for other := range seen {
			if isNested(s, other) || isNested(other, s) {
				return fmt.Errorf("shares %q and %q are nested in each other", s, other)
			}
		}
		seen[s] = true
	}
	return nil
}

// isNested reports whether child sits strictly below parent; identical paths
// are not nested.
func isNested(child, parent string) bool {
	if child == parent {
		return false
	}
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !hasDotDotPrefix(rel)
}

func hasDotDotPrefix(rel string) bool {
	return len(rel) >= 2 && rel[:2] == ".."
}

// EffectiveCPUs resolves CPUs==0 ("all cores") to the host's core count; the
// VM spec needs a concrete number.
func EffectiveCPUs(cfg vmtypes.Config) int {
	if cfg.CPUs == 0 {
		return runtime.NumCPU()
	}
	return cfg.CPUs
}
