package serve

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

const (
	hypervisorEntitlement = "com.apple.security.hypervisor"
	// HelperName is the signed binary that runs the VM (cmd/barelyvm).
	HelperName = "barelyvm"
)

var (
	entitlementOnce sync.Once
	entitlementErr  error
)

// FindHelper returns the barelyvm binary to launch: the one next to barely
// itself (an installed ~/.barely/bin, or out/bin when running a build from
// the repo), else the installed copy in binDir.
func FindHelper(barelyPath, binDir string) (string, error) {
	for _, dir := range []string{filepath.Dir(barelyPath), binDir} {
		p := filepath.Join(dir, HelperName)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s not found next to %s or in %s; build it with `make barelyvm` and run barely setup", HelperName, barelyPath, binDir)
}

// checkHypervisorEntitlement fails fast when the helper can't create a VM:
// Hypervisor.framework refuses binaries without the entitlement, and the
// VM process would otherwise die during boot with an opaque HVF error.
func checkHypervisorEntitlement(helper string) error {
	// Checked once per process: codesign is an exec and the helper doesn't
	// change while serving.
	entitlementOnce.Do(func() {
		entitlementErr = HasHypervisorEntitlement(helper)
	})
	return entitlementErr
}

// HasHypervisorEntitlement reports whether the binary at path is signed
// with the entitlement Hypervisor.framework requires.
func HasHypervisorEntitlement(path string) error {
	out, err := exec.Command("codesign", "-d", "--entitlements", "-", path).CombinedOutput()
	if err != nil || !strings.Contains(string(out), hypervisorEntitlement) {
		return fmt.Errorf("%s isn't signed with %s, so it can't run a VM; build it with `make barelyvm`", path, hypervisorEntitlement)
	}
	return nil
}
