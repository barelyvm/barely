package main

import (
	"bytes"
	"testing"
)

// dispatchCommands mirrors main's switch in main.go: every case there
// (including the hidden ones) must have a commandSpec with help text,
// and "barely help <cmd>" must work for it.
var dispatchCommands = []string{
	"serve", "man",
	"setup", "uninstall", "start", "stop", "restart",
	"status", "logs", "doctor", "config", "shell", "top",
}

// Help is generated from commandSpecs; these tests keep it in step with main's
// dispatch switch. Run with "go test ./cmd/barely".

// TestDispatchCommandsHaveHelp fails when a command is added to main without a
// commandSpec, which would leave it out of help and the man page.
func TestDispatchCommandsHaveHelp(t *testing.T) {
	for _, name := range dispatchCommands {
		c, ok := lookupCommand(name)
		if !ok {
			t.Errorf("command %q is dispatched in main.go but has no commandSpec", name)
			continue
		}
		if c.synopsis == "" {
			t.Errorf("command %q has no synopsis", name)
		}
		if c.long == "" {
			t.Errorf("command %q has no description", name)
		}
	}
}

// TestHelpCommandWorksForEveryDispatchedCommand covers hidden commands too,
// since "barely help serve" must keep working even though the overview omits it.
func TestHelpCommandWorksForEveryDispatchedCommand(t *testing.T) {
	for _, name := range dispatchCommands {
		var buf bytes.Buffer
		if err := printCommandHelp(&buf, name); err != nil {
			t.Errorf("barely help %s: %v", name, err)
			continue
		}
		if buf.Len() == 0 {
			t.Errorf("barely help %s: produced no output", name)
		}
	}
}

func TestHelpUnknownCommandErrors(t *testing.T) {
	var buf bytes.Buffer
	if err := printCommandHelp(&buf, "nonexistent"); err == nil {
		t.Error("expected an error for an unknown command")
	}
}

// TestOverviewHidesInternalCommands keeps serve and man, which users never
// run themselves, out of the overview.
func TestOverviewHidesInternalCommands(t *testing.T) {
	var buf bytes.Buffer
	printOverview(&buf)
	out := buf.String()
	for _, hidden := range []string{"serve", "man"} {
		if bytes.Contains(buf.Bytes(), []byte("  "+hidden+" ")) {
			t.Errorf("overview should hide internal command %q, got:\n%s", hidden, out)
		}
	}
	for _, shown := range []string{"setup", "status", "shell", "doctor"} {
		if !bytes.Contains([]byte(out), []byte(shown)) {
			t.Errorf("overview should list command %q, got:\n%s", shown, out)
		}
	}
}
