package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShellProfileInstallRemove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")
	rc := filepath.Join(home, ".zshrc")
	before := "export EDITOR=vim\nalias ll='ls -l'"
	if err := os.WriteFile(rc, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := detectShellProfile("/Users/x/.barely/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := p.install(); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := os.ReadFile(rc)
	want := before + "\n" + shellBlockStart + "\nexport DOCKER_HOST=unix:///Users/x/.barely/docker.sock\n" + shellBlockEnd + "\n"
	if string(got) != want {
		t.Errorf("after two installs:\n%q\nwant\n%q", got, want)
	}
	removed, err := p.remove()
	if err != nil || !removed {
		t.Fatalf("remove: removed=%v err=%v", removed, err)
	}
	got, _ = os.ReadFile(rc)
	if string(got) != before+"\n" {
		t.Errorf("after remove: %q, want %q", got, before+"\n")
	}
	if removed, _ := p.remove(); removed {
		t.Error("second remove reported a removal")
	}
}

func TestFishProfileIsItsOwnFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/opt/homebrew/bin/fish")
	p, err := detectShellProfile("/s/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.install(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.path); err != nil {
		t.Fatal(err)
	}
	if removed, err := p.remove(); err != nil || !removed {
		t.Fatalf("remove: %v %v", removed, err)
	}
	if _, err := os.Stat(p.path); !os.IsNotExist(err) {
		t.Error("fish conf file still exists after remove")
	}
}
