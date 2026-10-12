package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// manFlags holds the hidden "barely man"'s flags.
type manFlags struct {
	fs  *flag.FlagSet
	out *string
}

func newManFlags() *manFlags {
	fs := flag.NewFlagSet("man", flag.ExitOnError)
	f := &manFlags{fs: fs}
	f.out = fs.String("o", "", "write the man page to this path instead of stdout")
	return f
}

// runMan implements the hidden "barely man" subcommand: it renders the same
// help text behind "barely help" as a roff man page.
// `make man` calls
// this to write out/man/barely.1.
func runMan(args []string) error {
	flags := newManFlags()
	if err := flags.fs.Parse(args); err != nil {
		return err
	}

	page := renderManPage()
	if *flags.out == "" {
		_, err := os.Stdout.WriteString(page)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*flags.out), 0o755); err != nil {
		return err
	}
	return os.WriteFile(*flags.out, []byte(page), 0o644)
}

// renderManPage builds a barely(1) roff page from commandSpecs, skipping
// hidden (internal-only) commands.
func renderManPage() string {
	var b bytes.Buffer

	fmt.Fprintf(&b, ".TH BARELY 1 %q \"barely\" \"User Commands\"\n", time.Now().Format("2006-01-02"))
	fmt.Fprintln(&b, ".SH NAME")
	fmt.Fprintln(&b, "barely \\- open source Docker host VM with elastic memory and storage management")

	fmt.Fprintln(&b, ".SH SYNOPSIS")
	fmt.Fprintln(&b, ".B barely")
	fmt.Fprintln(&b, "\\fIcommand\\fR [flags]")

	fmt.Fprintln(&b, ".SH DESCRIPTION")
	fmt.Fprintln(&b, "barely runs the Linux VM that Docker needs on a Mac. The VM takes memory and disk space as")
	fmt.Fprintln(&b, "containers use them and hands them back to macOS afterwards, up to caps you set, instead of")
	fmt.Fprintln(&b, "holding a fixed share of the machine like Docker Desktop. See")
	fmt.Fprintln(&b, ".B barely doctor")
	fmt.Fprintln(&b, "for its coexistence with Docker Desktop.")

	fmt.Fprintln(&b, ".SH COMMANDS")
	var group string
	for _, c := range commandSpecs {
		if c.hidden {
			continue
		}
		if c.group != group {
			fmt.Fprintf(&b, ".SS %s\n", roffEscape(c.group))
			group = c.group
		}
		fmt.Fprintln(&b, ".TP")
		fmt.Fprintf(&b, ".B %s\n", roffEscape(c.synopsis))
		fmt.Fprintln(&b, roffEscape(c.long))

		if c.flags != nil {
			fs := c.flags()
			fs.VisitAll(func(f *flag.Flag) {
				fmt.Fprintln(&b, ".RS")
				fmt.Fprintf(&b, ".IP \\(bu\n\\fB\\-%s\\fR %s (default %q)\n", roffEscape(f.Name), roffEscape(f.Usage), f.DefValue)
				fmt.Fprintln(&b, ".RE")
			})
		}
		for _, ex := range c.examples {
			fmt.Fprintf(&b, ".br\n\\fI%s\\fR\n", roffEscape(ex))
		}
	}

	fmt.Fprintln(&b, ".SH FILES")
	fmt.Fprintln(&b, "~/.barely is the state directory: config.toml, docker.sock, logs/, guest images, images and volumes.")

	fmt.Fprintln(&b, ".SH SEE ALSO")
	fmt.Fprintln(&b, "docker(1)")

	return b.String()
}

// roffEscape neutralizes roff's special characters (backslash and a
// leading dot or single quote) in text pulled from Go strings that were
// never written with roff in mind.
func roffEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "-", `\-`)
	if strings.HasPrefix(s, ".") || strings.HasPrefix(s, "'") {
		s = `\&` + s
	}
	return s
}
