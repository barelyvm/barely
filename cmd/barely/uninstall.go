package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"

	"barely/internal/launchd"
	"barely/internal/state"
)

// uninstallFlags holds "barely uninstall"'s flags; newUninstallFlags is also
// used by "barely help uninstall" and "barely man" so the flag list can never
// drift from what uninstall actually parses.
type uninstallFlags struct {
	fs      *flag.FlagSet
	context *string
	symlink *string
	purge   *bool
}

func newUninstallFlags() *uninstallFlags {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	f := &uninstallFlags{fs: fs}
	f.context = fs.String("context", "barely", "Docker context name to remove")
	f.symlink = fs.String("symlink", "/var/run/docker.sock", "symlink to remove if it's still ours (empty: skip)")
	f.purge = fs.Bool("purge", false, "also delete the state directory")
	return f
}

// runUninstall implements "barely uninstall":
// reverses all of setup and, with -purge, deletes the state directory.
func runUninstall(args []string) error {
	flags := newUninstallFlags()
	if err := flags.fs.Parse(args); err != nil {
		return err
	}
	contextName := flags.context
	symlinkPath := flags.symlink
	purge := flags.purge

	dir, err := state.Dir()
	if err != nil {
		return err
	}
	paths := state.New(dir)
	defaultDir, err := state.DefaultDir()
	if err != nil {
		return err
	}
	label := launchd.LabelForStateDir(dir, defaultDir)

	if err := launchd.Bootout(label); err != nil {
		fmt.Printf("warning: %v\n", err)
	}
	if plistPath, err := launchd.PlistPath(label); err == nil {
		os.Remove(plistPath)
	}
	fmt.Printf("removed launchd agent %s\n", label)

	if profile, err := detectShellProfile(paths.DockerSock()); err == nil {
		if removed, err := profile.remove(); err != nil {
			fmt.Printf("warning: %v\n", err)
		} else if removed {
			fmt.Printf("removed the DOCKER_HOST line from %s\n", profile.path)
		}
	}

	if err := os.RemoveAll(paths.BinDir()); err != nil {
		fmt.Printf("warning: removing %s: %v\n", paths.BinDir(), err)
	} else {
		fmt.Printf("removed %s\n", paths.BinDir())
	}

	if out, err := exec.Command("docker", "context", "rm", "-f", *contextName).CombinedOutput(); err != nil {
		fmt.Printf("warning: removing docker context %q: %v: %s\n", *contextName, err, out)
	} else {
		fmt.Printf("removed docker context %q\n", *contextName)
	}

	// Only remove the link if it still points at our socket; it may since have
	// been re-pointed by Docker Desktop. sudo because setup created it with sudo.
	if *symlinkPath != "" {
		if target, err := os.Readlink(*symlinkPath); err == nil && target == paths.DockerSock() {
			out, err := exec.Command("sudo", "rm", "-f", *symlinkPath).CombinedOutput()
			if err != nil {
				fmt.Printf("warning: removing %s: %v: %s\n", *symlinkPath, err, out)
			} else {
				fmt.Printf("removed %s\n", *symlinkPath)
			}
		}
	}

	if *purge {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("barely uninstall: removing %s: %w", dir, err)
		}
		fmt.Printf("removed %s\n", dir)
	} else {
		fmt.Printf("state directory %s left in place (-purge to remove it)\n", dir)
	}

	fmt.Println("uninstall complete")
	return nil
}
