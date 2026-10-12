package main

import (
	"os"
	"path/filepath"
	"testing"

	"barely/internal/config"
	"barely/internal/state"
)

// Setup's tests run against temp state directories and a pipe standing in for
// stdin, so they never touch ~/.barely, docker or launchd. Run with "go test
// ./cmd/barely".

// TestWriteConfigKeys pins that setup edits config.toml key by key: a given
// key is replaced, keys the user set stay, and no keys writes no file.
func TestWriteConfigKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")

	if err := writeConfigKeys(path, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("no keys should leave no file, stat err = %v", err)
	}

	if err := os.WriteFile(path, []byte("cpus = 2\nmemory_max = \"4GiB\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeConfigKeys(path, map[string]string{"memory_max": "12GiB"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MemoryMax != 12<<30 {
		t.Errorf("memory_max = %d, want 12 GiB", cfg.MemoryMax)
	}
	if cfg.CPUs != 2 {
		t.Errorf("cpus = %d, want the existing 2 kept", cfg.CPUs)
	}
	if cfg.DiskMax != config.Default().DiskMax {
		t.Errorf("disk_max = %d, want the default since it was never set", cfg.DiskMax)
	}
}

// answer feeds lines to stdin for the duration of the test.
func answer(t *testing.T, lines string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.WriteString(lines)
	w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old })
}

// TestSetupCaps covers the memory and disk cap choices, including the rule
// that a disk which already exists can be raised but never lowered.
func TestSetupCaps(t *testing.T) {
	def := config.Default()

	t.Run("enter keeps the defaults and writes nothing", func(t *testing.T) {
		paths := state.New(t.TempDir())
		answer(t, "\n\n")
		if err := setupCaps(&setupUI{interactive: true}, newSetupFlags(), paths); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(paths.Config()); !os.IsNotExist(err) {
			t.Fatalf("config.toml should not exist, stat err = %v", err)
		}
	})

	t.Run("answers are written, a bad one is asked again", func(t *testing.T) {
		paths := state.New(t.TempDir())
		answer(t, "lots\n12\n40g\n")
		if err := setupCaps(&setupUI{interactive: true}, newSetupFlags(), paths); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(paths.Config())
		if err != nil {
			t.Fatal(err)
		}
		if cfg.MemoryMax != 12<<30 || cfg.DiskMax != 40<<30 {
			t.Errorf("got memory %d disk %d, want 12 GiB and 40 GiB", cfg.MemoryMax, cfg.DiskMax)
		}
	})

	// A sparse 2 GiB data.img, as if the disk had been created at that cap.
	withDisk := func(t *testing.T) state.Paths {
		t.Helper()
		paths := state.New(t.TempDir())
		if err := os.WriteFile(paths.Config(), []byte("disk_max = \"2G\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(createEmpty(t, paths.DataImage()), 2<<30); err != nil {
			t.Fatal(err)
		}
		return paths
	}

	t.Run("an existing disk can be raised by flag", func(t *testing.T) {
		paths := withDisk(t)
		flags := newSetupFlags()
		if err := flags.fs.Parse([]string{"-memory-max", "6", "-disk-max", "4G"}); err != nil {
			t.Fatal(err)
		}
		if err := setupCaps(&setupUI{}, flags, paths); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(paths.Config())
		if err != nil {
			t.Fatal(err)
		}
		if cfg.MemoryMax != 6<<30 || cfg.DiskMax != 4<<30 {
			t.Errorf("got memory %d disk %d, want 6 GiB and 4 GiB", cfg.MemoryMax, cfg.DiskMax)
		}
	})

	t.Run("an existing disk can't be lowered by flag", func(t *testing.T) {
		paths := withDisk(t)
		flags := newSetupFlags()
		if err := flags.fs.Parse([]string{"-disk-max", "1G"}); err != nil {
			t.Fatal(err)
		}
		if err := setupCaps(&setupUI{}, flags, paths); err == nil {
			t.Fatal("want an error")
		}
	})

	t.Run("an existing disk: a smaller answer is asked again", func(t *testing.T) {
		paths := withDisk(t)
		answer(t, "\n1G\n3G\n")
		if err := setupCaps(&setupUI{interactive: true}, newSetupFlags(), paths); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(paths.Config())
		if err != nil {
			t.Fatal(err)
		}
		if cfg.DiskMax != 3<<30 {
			t.Errorf("disk_max = %d, want 3 GiB", cfg.DiskMax)
		}
		if cfg.MemoryMax != def.MemoryMax {
			t.Errorf("memory_max = %d, want the default", cfg.MemoryMax)
		}
	})

	t.Run("a cap below 1 GiB is refused", func(t *testing.T) {
		paths := state.New(t.TempDir())
		flags := newSetupFlags()
		if err := flags.fs.Parse([]string{"-memory-max", "512MiB"}); err != nil {
			t.Fatal(err)
		}
		if err := setupCaps(&setupUI{}, flags, paths); err == nil {
			t.Fatal("want an error")
		}
	})
}

func createEmpty(t *testing.T, path string) string {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
