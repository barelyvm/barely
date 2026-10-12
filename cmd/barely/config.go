package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/pelletier/go-toml/v2"

	"barely/internal/config"
	"barely/internal/state"
)

// runConfig implements "barely config": with no args, prints the effective
// config (defaults filled in); "barely config edit" opens config.toml (the
// only file users edit) in $EDITOR, creating it with the
// defaults first if it doesn't exist yet. Changes apply on the next VM
// boot, or immediately with "barely restart".
func runConfig(args []string) error {
	paths, err := cliPaths()
	if err != nil {
		return err
	}

	if len(args) > 0 && args[0] == "edit" {
		return editConfig(paths)
	}

	cfg, err := config.Load(paths.Config())
	if err != nil {
		return err
	}
	body, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	fmt.Printf("# %s\n%s", paths.Config(), body)
	return nil
}

// editConfig validates after the editor exits because nothing else would: a
// broken config.toml otherwise only shows up when the host process next fails
// to boot the VM.
func editConfig(paths state.Paths) error {
	if _, err := os.Stat(paths.Config()); os.IsNotExist(err) {
		cfg := config.Default()
		body, err := toml.Marshal(cfg)
		if err != nil {
			return err
		}
		if err := os.WriteFile(paths.Config(), body, 0o600); err != nil {
			return err
		}
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command(editor, paths.Config())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	if _, err := config.Load(paths.Config()); err != nil {
		return fmt.Errorf("barely config: %w (your edits weren't validated; fix and run 'barely config edit' again)", err)
	}
	return nil
}
