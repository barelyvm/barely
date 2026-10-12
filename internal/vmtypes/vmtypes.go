// Package vmtypes holds the data shapes shared between the host process and
// the VM process: the user
// Config, the guest image's manifest, and the VMSpec the host process hands
// the VM process on stdin.
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

// Config is the user-editable config (config.toml).
type Config struct {
	MemoryMax    ByteSize `toml:"memory_max"`
	CPUs         int      `toml:"cpus"`
	DiskMax      ByteSize `toml:"disk_max"`
	IdleTimeout  Duration `toml:"idle_timeout"`
	Shares       []string `toml:"shares"`
	StartAtLogin bool     `toml:"start_at_login"`
}

// GuestManifest ships with each guest image.
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

// VMSpec is what the host process hands the VM process on stdin.
type VMSpec struct {
	CPUs        int
	MemoryBytes uint64
	Kernel      string
	Initrd      string
	Cmdline     string
	RootImage   string
	DataImage   string
	DataCap     uint64
	Shares      []Share
	Vsock       []VsockPort
	ConsoleLog  string
}

type Share struct {
	HostPath string
	Tag      string
}

type VsockPort struct {
	Port   uint32
	Path   string
	Listen bool
}

// MemStats is reported by the agent every second.
type MemStats struct {
	TotalBytes     uint64  `json:"total_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	FreeBytes      uint64  `json:"free_bytes"`
	CacheBytes     uint64  `json:"cache_bytes"`
	PSISomeAvg10   float64 `json:"psi_some_avg10"`
}
