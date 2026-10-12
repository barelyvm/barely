package guestargs

import (
	"reflect"
	"testing"

	"barely/internal/vmtypes"
)

// TestRoundTrip pins that Parse recovers what Encode wrote from inside a
// full kernel command line, including paths with a space and a colon, since
// the agent reads these words back out of /proc/cmdline. Run with go test.
func TestRoundTrip(t *testing.T) {
	for _, args := range []Args{
		{
			Shares: []vmtypes.Share{
				{HostPath: "/Users", Tag: "share0"},
				{HostPath: "/Volumes/My Disk:2", Tag: "share1"},
			},
			DockerSockAliases: []string{"/Users/a b/.barely/docker.sock"},
			DataImage:         "/Users/a b/.barely/data.img",
		},
	} {
		cmdline := "console=hvc0 page_reporting.page_reporting_order=9 " + args.Encode()
		got := Parse(cmdline)
		if !reflect.DeepEqual(got, args) {
			t.Errorf("Parse(%q) = %+v, want %+v", cmdline, got, args)
		}
	}
}
