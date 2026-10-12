package guestargs

import (
	"reflect"
	"testing"

	"barely/internal/vmtypes"
)

func TestRoundTrip(t *testing.T) {
	for _, args := range []Args{
		{
			Shares: []vmtypes.Share{
				{HostPath: "/Users", Tag: "share0"},
				{HostPath: "/Volumes/My Disk:2", Tag: "share1"},
			},
		},
	} {
		cmdline := "console=hvc0 page_reporting.page_reporting_order=9 " + args.Encode()
		got := Parse(cmdline)
		if !reflect.DeepEqual(got, args) {
			t.Errorf("Parse(%q) = %+v, want %+v", cmdline, got, args)
		}
	}
}
