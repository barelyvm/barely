// Package guestimg finds the guest image to boot: ~/.barely/guest/<version>/.
// `make guest` installs new
// versions there; old ones are pruned after a successful boot.
package guestimg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"barely/internal/vmtypes"
)

// Image is one guest/<version>/ directory and its parsed manifest.
type Image struct {
	Dir      string
	Manifest vmtypes.GuestManifest
}

func (img Image) Kernel() string    { return filepath.Join(img.Dir, "Image") }
func (img Image) Initrd() string    { return filepath.Join(img.Dir, "initrd.img") }
func (img Image) RootImage() string { return filepath.Join(img.Dir, "root.img") }

// Find returns the newest guest image under guestDir whose AgentProtocol
// matches wantProtocol. Versions are compared lexically on their directory
// name, which is fine for the "YYYY.MM.N" scheme the manifest documents.
func Find(guestDir string, wantProtocol int) (Image, error) {
	entries, err := os.ReadDir(guestDir)
	if err != nil {
		return Image{}, fmt.Errorf("guestimg: reading %s: %w", guestDir, err)
	}
	var versions []string
	for _, e := range entries {
		if e.IsDir() {
			versions = append(versions, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(versions)))

	var lastErr error
	for _, v := range versions {
		img, err := Open(filepath.Join(guestDir, v), wantProtocol)
		if err != nil {
			lastErr = err
			continue
		}
		return img, nil
	}
	if lastErr != nil {
		return Image{}, lastErr
	}
	return Image{}, fmt.Errorf("guestimg: no guest image found under %s", guestDir)
}

// Open reads the guest image in dir. An agent speaking an older protocol
// may not understand what this host asks of it, so it's refused with a
// pointer to rebuilding.
func Open(dir string, wantProtocol int) (Image, error) {
	m, err := readManifest(dir)
	if err != nil {
		return Image{}, err
	}
	if m.AgentProtocol < wantProtocol {
		return Image{}, fmt.Errorf("guestimg: guest image %s too old (agent_protocol %d, want %d) — run `make guest`", dir, m.AgentProtocol, wantProtocol)
	}
	if m.AgentProtocol != wantProtocol {
		return Image{}, fmt.Errorf("guestimg: guest image %s is newer than this barely (agent_protocol %d, want %d)", dir, m.AgentProtocol, wantProtocol)
	}
	return Image{Dir: dir, Manifest: m}, nil
}

func readManifest(dir string) (vmtypes.GuestManifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return vmtypes.GuestManifest{}, err
	}
	var m vmtypes.GuestManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return vmtypes.GuestManifest{}, fmt.Errorf("guestimg: %s: bad manifest.json: %w", dir, err)
	}
	return m, nil
}
