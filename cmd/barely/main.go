// Command barely is the elastic Docker VM: run as a user it's the CLI, run by
// launchd as "barely serve" it's the host process, and run by "barely serve" as
// "barely vm" it's the VM process.
package main

import (
	"fmt"
	"os"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		printOverview(os.Stdout)
		return
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	// "barely help", "barely -h" and "barely --help" all print the overview (or,
	// with a command name, that command's own help); "barely <command>
	// -h/--help" prints that command's help too, same text either way.
	wantsHelp := cmd == "help" || cmd == "-h" || cmd == "--help"
	if wantsHelp && len(args) > 0 {
		cmd, args = args[0], nil
	}
	if !wantsHelp && len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		wantsHelp = true
	}

	if wantsHelp {
		if cmd == "help" || cmd == "-h" || cmd == "--help" {
			printOverview(os.Stdout)
			return
		}
		if err := printCommandHelp(os.Stdout, cmd); err != nil {
			fmt.Fprintln(os.Stderr, "barely:", err)
			fmt.Fprintln(os.Stderr)
			printOverview(os.Stderr)
			os.Exit(2)
		}
		return
	}

	var err error
	switch cmd {
	case "version", "--version":
		fmt.Println("barely", version)
		return
	case "serve":
		err = runServe(args)
	case "man":
		err = runMan(args)
	case "setup":
		err = runSetup(args)
	case "uninstall":
		err = runUninstall(args)
	case "start":
		err = runStart(args)
	case "stop":
		err = runStop(args)
	case "restart":
		err = runRestart(args)
	case "status":
		err = runStatus(args)
	case "logs":
		err = runLogs(args)
	case "doctor":
		err = runDoctor(args)
	case "config":
		err = runConfig(args)
	case "shell":
		err = runShell(args)
	case "top":
		err = runTop(args)
	default:
		fmt.Fprintf(os.Stderr, "barely: unknown command %q\n\n", cmd)
		printOverview(os.Stderr)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "barely:", err)
		os.Exit(1)
	}
}
