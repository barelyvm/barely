package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"barely/internal/dockerdesktop"
	"barely/internal/guestimg"
	"barely/internal/launchd"
	"barely/internal/proto"
	"barely/internal/serve"
	"barely/internal/state"
)

// runDoctor implements "barely doctor": the checks for why docker isn't
// reaching barely. It reports the Mac-side dependencies, where
// /var/run/docker.sock points, the active Docker context, whether Docker
// Desktop's VM is running, the launchd agent and the binary and helper it
// runs, the installed guest image, and the VM state.
func runDoctor(args []string) error {
	dir, err := state.Dir()
	if err != nil {
		return err
	}
	paths := state.New(dir)

	fmt.Println("barely doctor")
	fmt.Println()

	printDeps(checkDeps())
	fmt.Println()

	symlink := "/var/run/docker.sock"
	target, err := os.Readlink(symlink)
	switch {
	case err != nil:
		fmt.Printf("%s: not a symlink (%v)\n", symlink, err)
	case target == paths.DockerSock():
		fmt.Printf("%s -> %s (ours)\n", symlink, target)
	default:
		fmt.Printf("%s -> %s (not ours", symlink, target)
		if strings.Contains(target, ".docker") {
			fmt.Print(" — looks like Docker Desktop re-took it; see its \"Allow the default Docker socket\" setting")
		}
		fmt.Println(")")
	}

	if out, err := exec.Command("docker", "context", "show").Output(); err == nil {
		fmt.Printf("active docker context: %s", out)
	}

	if pid, ok := dockerdesktop.PID(); ok {
		fmt.Printf("Docker Desktop VM: running (pid %d)\n", pid)
	} else {
		fmt.Println("Docker Desktop VM: not running")
	}

	label := launchd.LabelForStateDir(dir, mustDefaultDir())
	fmt.Printf("launchd agent: %s\n", label)
	if out, err := exec.Command("launchctl", "print", "gui/"+uidString()+"/"+label).CombinedOutput(); err == nil {
		fmt.Println("  loaded")
	} else {
		fmt.Printf("  not loaded: %s\n", strings.TrimSpace(string(out)))
	}

	if plist, err := launchd.PlistPath(label); err == nil {
		if out, err := exec.Command("plutil", "-extract", "ProgramArguments.0", "raw", plist).Output(); err == nil {
			bin := strings.TrimSpace(string(out))
			switch {
			// `go run` builds into a temp dir that is deleted when it exits, so an
			// agent plist pointing there would break.
			case strings.Contains(bin, "/go-build/"):
				fmt.Printf("  runs %s — a temporary `go run` build; re-run barely setup to install a stable copy into ~/.barely/bin\n", bin)
			default:
				fmt.Printf("  runs %s\n", bin)
				if helper, err := serve.FindHelper(bin, paths.BinDir()); err != nil {
					fmt.Printf("  %v\n", err)
				} else if err := serve.HasHypervisorEntitlement(helper); err != nil {
					fmt.Printf("  %v\n", err)
				} else {
					fmt.Printf("  %s (signed)\n", helper)
				}
			}
		}
	}

	if img, err := guestimg.Find(paths.GuestDir(), proto.Version); err != nil {
		fmt.Printf("guest image: %v\n", err)
	} else {
		fmt.Printf("guest image: %s (agent protocol %d)\n", img.Manifest.Version, img.Manifest.AgentProtocol)
	}

	st, err := fetchStatus(paths)
	if err == nil {
		fmt.Printf("barely VM: %s\n", st.State)
	}
	return nil
}

func uidString() string {
	return strconv.Itoa(os.Getuid())
}

func mustDefaultDir() string {
	d, err := state.DefaultDir()
	if err != nil {
		return ""
	}
	return d
}
