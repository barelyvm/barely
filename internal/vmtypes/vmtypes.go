// Package vmtypes holds the data shapes shared between the host process, the
// VM process and the guest agent: the user Config, the guest image's
// manifest, the VMSpec the host process hands the VM process on stdin, and
// the MemStats the agent reports back. It has no logic of its own beyond
// parsing sizes and durations from config text, so the processes can share
// types without importing each other.
//
// Used by: internal/config, internal/serve, internal/vmproc, internal/krun,
// internal/guestimg, internal/guestargs, internal/control, internal/netstack,
// cmd/barelyvm, cmd/barely, cmd/barely-agent.
package vmtypes

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ByteSize is a size in bytes, read from config as "100 GiB", "512MiB" etc.
type ByteSize uint64

func (b ByteSize) String() string {
	return fmt.Sprintf("%d", uint64(b))
}

func (b *ByteSize) UnmarshalText(text []byte) error {
	v, err := ParseByteSize(string(text))
	if err != nil {
		return err
	}
	*b = v
	return nil
}

func (b ByteSize) MarshalText() ([]byte, error) {
	return []byte(formatByteSize(uint64(b))), nil
}

// ParseByteSize parses sizes like "100GiB", "512 MiB", "100GB", "1024".
// kb/mb/gb/tb are powers of 1000; kib/mib/gib/tib and the bare k/m/g/t are
// powers of 1024.
func ParseByteSize(s string) (ByteSize, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty byte size")
	}
	i := 0
	for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	numPart, unitPart := s[:i], strings.TrimSpace(strings.ToLower(s[i:]))
	n, err := strconv.ParseFloat(numPart, 64)
	if err != nil {
		return 0, fmt.Errorf("bad byte size %q: %w", s, err)
	}
	var mult float64
	switch unitPart {
	case "", "b":
		mult = 1
	case "kb":
		mult = 1e3
	case "mb":
		mult = 1e6
	case "gb":
		mult = 1e9
	case "tb":
		mult = 1e12
	case "kib", "k":
		mult = 1 << 10
	case "mib", "m":
		mult = 1 << 20
	case "gib", "g":
		mult = 1 << 30
	case "tib", "t":
		mult = 1 << 40
	default:
		return 0, fmt.Errorf("bad byte size unit %q in %q", unitPart, s)
	}
	return ByteSize(n * mult), nil
}

// formatByteSize writes exact GiB/MiB multiples as "8G"/"512M" so a
// marshalled config stays readable, and anything else as exact bytes so
// ParseByteSize round-trips it.
func formatByteSize(b uint64) string {
	const gib, mib = 1 << 30, 1 << 20
	if b != 0 && b%gib == 0 {
		return fmt.Sprintf("%dG", b/gib)
	}
	if b != 0 && b%mib == 0 {
		return fmt.Sprintf("%dM", b/mib)
	}
	return fmt.Sprintf("%dB", b)
}

// Duration is time.Duration with text (un)marshalling, so config.toml can
// write "5m" instead of a raw nanosecond count: encoding/json and
// go-toml/v2 both use encoding.TextUnmarshaler when a type implements it,
// which time.Duration itself doesn't.
type Duration time.Duration

func (d Duration) String() string { return time.Duration(d).String() }

func (d *Duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

// Config is the user-editable config (config.toml). internal/config loads and
// validates it; internal/serve sizes the VM from it each boot.
type Config struct {
	MemoryMax    ByteSize `toml:"memory_max"`
	CPUs         int      `toml:"cpus"`
	DiskMax      ByteSize `toml:"disk_max"`
	IdleTimeout  Duration `toml:"idle_timeout"`
	Shares       []string `toml:"shares"`
	StartAtLogin bool     `toml:"start_at_login"`
	// Backup includes the data disk in Time Machine backups. Off by default:
	// see diskimg.SetBackup.
	Backup bool `toml:"backup"`
}

// GuestManifest is manifest.json, written by `make guest` into each guest
// image directory and read by guestimg. AgentProtocol decides whether the
// image is usable by this host; Kernel, FseventABI and SHA256 are recorded
// but not read by any Go code.
type GuestManifest struct {
	Version       string            `json:"version"`
	Kernel        string            `json:"kernel"`
	AgentProtocol int               `json:"agent_protocol"`
	FseventABI    int               `json:"fsevent_abi"`
	SHA256        map[string]string `json:"sha256"`
}

// GuestMAC and GatewayMAC are the fixed MAC addresses of, respectively, the
// guest's virtio-net device (krun_add_net_unixgram, internal/krun) and
// gvisor-tap-vsock's virtual gateway (internal/netstack) on the private
// network between them. They must differ: a shared MAC on both ends of the
// same L2 segment broke DHCP (the guest's request and the
// gateway's own traffic looked like they came from the same address).
const (
	GuestMAC   = "5a:94:ef:e4:0c:ee"
	GatewayMAC = "5a:94:ef:e4:0c:ed"
)

// VMSpec is everything needed to boot the guest, built by internal/serve,
// sent as JSON on the VM process's stdin (internal/vmproc), checked by
// barelyvm and applied by internal/krun. Paths are absolute host paths.
type VMSpec struct {
	CPUs        int
	MemoryBytes uint64
	Kernel      string
	Initrd      string // optional
	Cmdline     string // kernel command line, including the guestargs words
	RootImage   string // attached read-only as the first disk
	DataImage   string // attached read-write as the second disk
	DataCap     uint64 // set by serve but read by nothing
	Shares      []Share
	Vsock       []VsockPort
	ConsoleLog  string // guest console output file; empty leaves libkrun's default, the VM process's own stdio
}

// Share is one host directory exposed to the guest over virtio-fs. Tag is the
// virtio-fs mount tag; guestargs passes tag and path to the agent so it mounts
// the share at the same path.
type Share struct {
	HostPath string
	Tag      string
}

// VsockPort maps a guest vsock port to a host unix socket at Path. With
// Listen the guest listens and the host initiates by connecting to Path
// (libkrun's "listen" flag).
type VsockPort struct {
	Port   uint32
	Path   string
	Listen bool
}

// MemStats is the guest's own memory view, reported by the agent every second
// and shown by `barely status` and `barely top`. PSISomeAvg10 is the kernel's
// pressure-stall "some" 10-second average, 0 when PSI is unavailable.
type MemStats struct {
	TotalBytes     uint64  `json:"total_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	FreeBytes      uint64  `json:"free_bytes"`
	CacheBytes     uint64  `json:"cache_bytes"`
	PSISomeAvg10   float64 `json:"psi_some_avg10"`
}
