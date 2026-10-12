package main

import (
	"flag"
	"fmt"
	"io"
)

// commandSpec documents one barely subcommand. It backs "barely help", "barely
// <command> -h/--help" and "barely man", all from one
// place so the three never say different things. flags, when non-nil,
// returns the command's real flag.FlagSet (the same constructor the
// command itself parses with), so the flag list in help can never drift
// from the flags the command actually accepts.
type commandSpec struct {
	name     string
	hidden   bool // excluded from "barely help"'s overview and the man page
	group    string
	synopsis string
	short    string // one line, for the overview
	long     string // 1-3 sentences, for the command's own help
	examples []string
	flags    func() *flag.FlagSet
}

// commandSpecs is the help text for every dispatched command, in display
// order. printOverview and renderManPage print a group heading whenever the
// group changes, so entries of one group must stay adjacent.
var commandSpecs = []commandSpec{
	{
		name:     "setup",
		group:    "Lifecycle",
		synopsis: "barely setup [flags]",
		short:    "Install barely and point Docker at it, step by step",
		long: "Walks through each check and each change it makes: the docker CLI pieces barely needs, installing " +
			"barely into ~/.barely/bin, the memory and disk caps, the launchd agent that starts the VM on demand, the Docker context, the " +
			"/var/run/docker.sock symlink, and an optional DOCKER_HOST line in your shell profile. It explains " +
			"each step and asks at each choice you didn't give as a flag. With -defaults it asks nothing and " +
			"every choice you didn't specify takes its default.",
		examples: []string{"barely setup", "barely setup -memory-max 12G", "barely setup -defaults", "barely setup -defaults -memory-max 12G -symlink= -select-context=false"},
		flags:    func() *flag.FlagSet { return newSetupFlags().fs },
	},
	{
		name:     "uninstall",
		group:    "Lifecycle",
		synopsis: "barely uninstall [flags]",
		short:    "Reverse setup",
		long: "Removes the launchd agent, the installed binaries in ~/.barely/bin, the DOCKER_HOST line in your shell " +
			"profile, the Docker context, and the /var/run/docker.sock symlink if it's still ours. The rest of " +
			"~/.barely (images, volumes, logs, config) is left in place unless -purge is given.",
		examples: []string{"barely uninstall", "barely uninstall -purge"},
		flags:    func() *flag.FlagSet { return newUninstallFlags().fs },
	},
	{
		name:     "start",
		group:    "Lifecycle",
		synopsis: "barely start",
		short:    "Boot the VM",
		long: "Boots the VM the same way any docker call does, by connecting to docker.sock: if the host " +
			"process isn't running, launchd's socket activation starts it. Blocks until the VM reports running.",
	},
	{
		name:     "stop",
		group:    "Lifecycle",
		synopsis: "barely stop",
		short:    "Clean shutdown now",
		long:     "Shuts the VM down cleanly. The next docker command (or barely start) boots it again.",
	},
	{
		name:     "restart",
		group:    "Lifecycle",
		synopsis: "barely restart",
		short:    "Clean shutdown, then boot again with the config reloaded",
		long:     "Shuts the VM down cleanly and boots it again, picking up any changes made to config.toml.",
	},
	{
		name:     "status",
		group:    "Visibility",
		synopsis: "barely status",
		short:    "VM state, memory footprint, guest/disk stats",
		long: "Prints the VM's state, its memory footprint now vs its recent peak, guest used/cache memory, " +
			"and disk usage.",
	},
	{
		name:     "top",
		group:    "Visibility",
		synopsis: "barely top",
		short:    "Live, 1s-refreshed view of status plus per-container memory/CPU",
		long: "A live terminal view refreshed every second: the same footprint, guest and disk stats as " +
			"barely status, plus a memory history sparkline and per-container memory and CPU usage.",
	},
	{
		name:     "logs",
		group:    "Visibility",
		synopsis: "barely logs [flags]",
		short:    "Host process log, VM process log, or guest console",
		long: "Prints a log file directly, which works even when the host process isn't running. " +
			"-follow keeps reading as the file grows, like tail -f.",
		examples: []string{"barely logs", "barely logs -source vm", "barely logs -source console -follow"},
		flags:    func() *flag.FlagSet { return newLogsFlags().fs },
	},
	{
		name:     "shell",
		group:    "Interact",
		synopsis: "barely shell [-- cmd args...]",
		short:    "Root shell (or one-off command) in the guest, no SSH",
		long: "Boots the VM if needed, then opens an interactive root shell in the guest. With -- cmd args..., " +
			"runs that one command instead and exits with its exit code. Terminal resize and Ctrl-C work.",
		examples: []string{"barely shell", "barely shell -- ls -la /var/lib/docker"},
	},
	{
		name:     "config",
		group:    "Config & diagnostics",
		synopsis: "barely config [edit]",
		short:    "Print or edit config.toml",
		long: "With no arguments, prints the effective config (defaults filled in). " +
			"\"barely config edit\" opens ~/.barely/config.toml in $EDITOR, creating it with the defaults first " +
			"if it doesn't exist. Changes apply on the next VM boot, or immediately with barely restart.",
		examples: []string{"barely config", "barely config edit"},
	},
	{
		name:     "doctor",
		group:    "Config & diagnostics",
		synopsis: "barely doctor",
		short:    "Check docker CLIs, the socket, the context, and Docker Desktop coexistence",
		long: "Reports where /var/run/docker.sock points, the active Docker context, whether Docker " +
			"Desktop's VM is running, whether the launchd agent is loaded, and the barely VM's own state.",
	},
	{
		name:     "serve",
		hidden:   true,
		synopsis: "barely serve [flags]",
		short:    "Run the host process (internal: launched by launchd)",
		long: "The host process: supervises the VM process, listens on docker.sock, and shuts the guest " +
			"down cleanly on SIGTERM. Not meant to be run by hand; barely setup installs it under launchd.",
		flags: func() *flag.FlagSet { return newServeFlags().fs },
	},
	{
		name:     "man",
		hidden:   true,
		synopsis: "barely man [flags]",
		short:    "Generate the barely(1) man page (internal: used by `make man`)",
		long:     "Renders barely's help text (the same text behind \"barely help\") as a roff man page.",
		examples: []string{"barely man", "barely man -o out/man/barely.1"},
		flags:    func() *flag.FlagSet { return newManFlags().fs },
	},
}

// lookupCommand finds a commandSpec by name, including hidden ones:
// "barely help <hidden-command>" still works, it's just left off the
// overview and the man page.
func lookupCommand(name string) (commandSpec, bool) {
	for _, c := range commandSpecs {
		if c.name == name {
			return c, true
		}
	}
	return commandSpec{}, false
}

// printOverview prints the short, grouped summary "barely help" (and bare
// barely, barely -h, barely --help) show.
func printOverview(w io.Writer) {
	fmt.Fprintln(w, "barely — open source Docker host VM with elastic memory and storage management")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  barely <command> [flags]")

	var group string
	for _, c := range commandSpecs {
		if c.hidden {
			continue
		}
		if c.group != group {
			fmt.Fprintln(w)
			fmt.Fprintf(w, "%s:\n", c.group)
			group = c.group
		}
		fmt.Fprintf(w, "  %-10s %s\n", c.name, c.short)
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run 'barely help <command>' for details on a command.")
}

// printCommandHelp prints one command's synopsis, description, flags
// (from its real flag.FlagSet) and examples. It works for hidden
// commands too; they're just left off the overview.
func printCommandHelp(w io.Writer, name string) error {
	c, ok := lookupCommand(name)
	if !ok {
		return fmt.Errorf("unknown command %q", name)
	}

	fmt.Fprintln(w, c.synopsis)
	fmt.Fprintln(w)
	fmt.Fprintln(w, c.long)

	if c.flags != nil {
		fs := c.flags()
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Flags:")
		fs.SetOutput(w)
		fs.PrintDefaults()
	}

	if len(c.examples) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Examples:")
		for _, ex := range c.examples {
			fmt.Fprintf(w, "  %s\n", ex)
		}
	}
	return nil
}
