package launchd

import (
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// defaultLabel is the production LaunchAgent label.
const defaultLabel = "dev.barely.serve"

// LabelForStateDir returns the LaunchAgent label to use for stateDir. A
// non-default state directory (BARELY_HOME set, e.g. for development) gets
// its own label derived from the directory, so a test install can never
// bootstrap, bootout or overwrite the real one.
func LabelForStateDir(stateDir, defaultStateDir string) string {
	if stateDir == defaultStateDir {
		return defaultLabel
	}
	h := fnv.New32a()
	h.Write([]byte(stateDir))
	return fmt.Sprintf("%s.%08x", defaultLabel, h.Sum32())
}

// AgentsDir is where per-user LaunchAgents live (launchd loads them for the
// logged-in user without root).
func AgentsDir() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return filepath.Join(u.HomeDir, "Library", "LaunchAgents"), nil
}

// PlistPath returns the LaunchAgent plist path for label.
func PlistPath(label string) (string, error) {
	dir, err := AgentsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, label+".plist"), nil
}

// plistTemplate is the LaunchAgent plist template. launchd owns the docker
// socket (mode 0600, SockPathMode 384 decimal) and launches "barely serve"
// on the first connection; KeepAlive is off because the host process exits
// on its own when idle and must stay down until the next connection, and
// RunAtLoad is the start-at-login setting.
const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%[1]s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%[2]s</string>
		<string>serve</string>
		<string>-state-dir</string>
		<string>%[3]s</string>
	</array>
	<key>Sockets</key>
	<dict>
		<key>docker</key>
		<dict>
			<key>SockPathName</key>
			<string>%[4]s</string>
			<key>SockPathMode</key>
			<integer>384</integer>
		</dict>
	</dict>
	<key>RunAtLoad</key>
	<%[5]s/>
	<key>KeepAlive</key>
	<false/>
	<key>StandardErrorPath</key>
	<string>%[6]s</string>
</dict>
</plist>
`

// WritePlist writes the LaunchAgent plist for label to its standard path
// and returns that path.
func WritePlist(label, barelyPath, stateDir, dockerSockPath, stderrPath string, runAtLoad bool) (string, error) {
	dir, err := AgentsDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path, err := PlistPath(label)
	if err != nil {
		return "", err
	}
	runAtLoadStr := "false"
	if runAtLoad {
		runAtLoadStr = "true"
	}
	body := fmt.Sprintf(plistTemplate, label, barelyPath, stateDir, dockerSockPath, runAtLoadStr, stderrPath)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// guiTarget is the launchctl domain for the current user's login session,
// where LaunchAgents are loaded.
func guiTarget() string {
	return "gui/" + strconv.Itoa(os.Getuid())
}

// Bootstrap loads the plist at path into launchd (launchctl bootstrap).
func Bootstrap(path string) error {
	out, err := exec.Command("launchctl", "bootstrap", guiTarget(), path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl bootstrap: %w: %s", err, out)
	}
	return nil
}

// Bootout removes label from launchd (launchctl bootout). Not finding it
// loaded isn't an error: uninstall and a re-run of setup should be idempotent.
func Bootout(label string) error {
	target := guiTarget() + "/" + label
	out, err := exec.Command("launchctl", "bootout", target).CombinedOutput()
	if err != nil && !alreadyGone(out) {
		return fmt.Errorf("launchctl bootout: %w: %s", err, out)
	}
	// launchctl bootout returns before launchd has finished removing the job,
	// and a bootstrap of the same label in that window fails with "5:
	// Input/output error", so wait until the job is really gone.
	for i := 0; i < 50; i++ {
		if exec.Command("launchctl", "print", target).Run() != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("launchctl bootout: %s still loaded after 5s", label)
}

// alreadyGone recognises launchctl's output when the job isn't loaded, which
// Bootout treats as success.
func alreadyGone(out []byte) bool {
	s := string(out)
	return len(s) == 0 || strings.Contains(s, "Could not find") || strings.Contains(s, "No such process")
}
