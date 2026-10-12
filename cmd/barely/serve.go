package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"barely/internal/serve"
	"barely/internal/state"
)

// serveFlags holds "barely serve"'s flags; newServeFlags is also used by
// "barely help serve" so the flag list can never drift from what serve
// actually parses.
type serveFlags struct {
	fs       *flag.FlagSet
	stateDir *string
}

func newServeFlags() *serveFlags {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	f := &serveFlags{fs: fs}
	f.stateDir = fs.String("state-dir", "", "state directory (default ~/.barely)")
	return f
}

// runServe implements the "barely serve" subcommand: the host process.
func runServe(args []string) error {
	flags := newServeFlags()
	if err := flags.fs.Parse(args); err != nil {
		return err
	}

	dir := *flags.stateDir
	if dir == "" {
		d, err := state.Dir()
		if err != nil {
			return err
		}
		dir = d
	}
	if err := state.EnsureDirs(dir); err != nil {
		return err
	}

	paths := state.New(dir)
	logf, err := os.OpenFile(paths.ServeLog(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("barely serve: opening serve.log: %w", err)
	}
	log.SetOutput(logf)

	barelyPath, err := os.Executable()
	if err != nil {
		return err
	}

	srv, err := serve.New(barelyPath, dir)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		log.Println("barely serve: shutting down")
		cancel()
	}()

	return srv.Run(ctx)
}
