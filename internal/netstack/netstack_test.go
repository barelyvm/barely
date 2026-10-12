package netstack

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"golang.org/x/sys/unix"

	"barely/internal/vmtypes"
)

func TestDHCPOffer(t *testing.T) {
	nw, err := New()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vmFD, closeGateway, err := nw.Socketpair(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer closeGateway()
	defer unix.Close(vmFD)

	mac, err := net.ParseMAC(vmtypes.GuestMAC)
	if err != nil {
		t.Fatal(err)
	}
	discover, err := dhcpv4.NewDiscovery(mac)
	if err != nil {
		t.Fatal(err)
	}
	eth := &layers.Ethernet{SrcMAC: mac, DstMAC: layers.EthernetBroadcast, EthernetType: layers.EthernetTypeIPv4}
	ip := &layers.IPv4{Version: 4, TTL: 64, Protocol: layers.IPProtocolUDP, SrcIP: net.IPv4zero, DstIP: net.IPv4bcast}
	udp := &layers.UDP{SrcPort: 68, DstPort: 67}
	if err := udp.SetNetworkLayerForChecksum(ip); err != nil {
		t.Fatal(err)
	}
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	if err := gopacket.SerializeLayers(buf, opts, eth, ip, udp, gopacket.Payload(discover.ToBytes())); err != nil {
		t.Fatal(err)
	}
	if _, err := unix.Write(vmFD, buf.Bytes()); err != nil {
		t.Fatal(err)
	}

	tv := unix.NsecToTimeval(int64(2 * time.Second))
	if err := unix.SetsockoptTimeval(vmFD, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 2048)
	for {
		n, err := unix.Read(vmFD, frame)
		if err != nil {
			t.Fatalf("no DHCP offer: %v", err)
		}
		pkt := gopacket.NewPacket(frame[:n], layers.LayerTypeEthernet, gopacket.Default)
		l, ok := pkt.Layer(layers.LayerTypeDHCPv4).(*layers.DHCPv4)
		if !ok {
			continue
		}
		if got := l.YourClientIP.String(); got != deviceIP {
			t.Fatalf("offered %s, want %s", got, deviceIP)
		}
		return
	}
}

// TestExposeUnexpose exercises the in-process forwarder calls without a real VM:
// expose a port, confirm a second Expose on the same local address conflicts
// (the Mac bind failure dockerd's published port would hit), then Unexpose
// and confirm the address is free again.
func TestExposeUnexpose(t *testing.T) {
	nw, err := New()
	if err != nil {
		t.Fatal(err)
	}

	local := "127.0.0.1:18080"
	remote := DeviceIP + ":8080"
	if err := nw.Expose("tcp", local, remote); err != nil {
		t.Fatalf("Expose: %v", err)
	}

	if err := nw.Expose("tcp", local, remote); err == nil {
		t.Fatal("Expose on an already-exposed local address: want an error, got nil")
	}

	if err := nw.Unexpose("tcp", local); err != nil {
		t.Fatalf("Unexpose: %v", err)
	}

	if err := nw.Expose("tcp", local, remote); err != nil {
		t.Fatalf("Expose after Unexpose: %v", err)
	}
	if err := nw.Unexpose("tcp", local); err != nil {
		t.Fatalf("Unexpose: %v", err)
	}
}
