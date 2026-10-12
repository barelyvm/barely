// Package guestimg finds the guest image to boot: ~/.barely/guest/<version>/.
// `make guest` installs new
// versions there. Only an image whose manifest agent_protocol equals the
// host's proto.Version is bootable, so an old image left behind by an
// upgrade is skipped or reported with a rebuild hint rather than booted.
//
// Entry point: Find. Open checks one directory and has no caller outside
// this package.
//
// Used by: internal/serve to boot, cmd/barely setup and doctor to check one exists.
package guestimg

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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

// Kernel, Initrd and RootImage are the files `make guest` puts in the image
// directory; they fill the matching VMSpec fields.
func (img Image) Kernel() string    { return filepath.Join(img.Dir, "Image") }
func (img Image) Initrd() string    { return filepath.Join(img.Dir, "initrd.img") }
func (img Image) RootImage() string { return filepath.Join(img.Dir, "root.img") }

// Find returns the newest guest image under guestDir whose AgentProtocol
// matches wantProtocol. Versions are compared lexically on their directory
// name, which is fine for the YYYY.MM.DD.HHMM versions `make guest` stamps.
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
	// Newest first; Find returns the first image the host can use.
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
	// Both mismatches are refused, but only the older one is fixed by
	// rebuilding the image.
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

// Verify checks each file the manifest lists against its recorded SHA-256, so
// a truncated or damaged image is caught when it is installed rather than as
// a guest that won't boot. Setup runs it; a boot does not, because hashing
// the image would be added to every cold start.
func (i Image) Verify() error {
	for name, want := range i.Manifest.SHA256 {
		f, err := os.Open(filepath.Join(i.Dir, name))
		if err != nil {
			return fmt.Errorf("guestimg: %w", err)
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return fmt.Errorf("guestimg: reading %s: %w", name, err)
		}
		if got := hex.EncodeToString(h.Sum(nil)); got != want {
			return fmt.Errorf("guestimg: %s in %s is damaged (sha256 %s, manifest says %s)", name, i.Dir, got, want)
		}
	}
	return nil
}
