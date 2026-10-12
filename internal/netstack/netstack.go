// Package netstack runs gvisor-tap-vsock's virtual network in-process, for
// the VM's networking: DHCP, outbound traffic, DNS resolved through macOS's
// resolver, host.docker.internal, and published ports. Running it in the
// host process rather than a separate gvproxy means the forwarder can be
// driven directly when containers publish ports.
//
// Entry points are New, Network.Socketpair (the VM's NIC) and
// Network.Expose/Unexpose (published ports).
//
// Used by: internal/serve, which owns the single Network and hands
// internal/portforward its Expose/Unexpose.
package netstack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/containers/gvisor-tap-vsock/pkg/types"
	"github.com/containers/gvisor-tap-vsock/pkg/virtualnetwork"
	"golang.org/x/sys/unix"

	"barely/internal/vmtypes"
)

const (
	subnet    = "192.168.127.0/24"
	gatewayIP = "192.168.127.1"
	deviceIP  = "192.168.127.2"

	// HostIP is the virtual address that resolves to the Mac itself:
	// gvisor-tap-vsock NATs traffic sent to it to 127.0.0.1, so a
	// container reaching HostIP reaches a server listening on the Mac's
	// loopback. host.docker.internal resolves here (below).
	HostIP = "192.168.127.254"

	hostDNSZone = "docker.internal."
)

// DeviceIP is the guest's address on the virtual network. Published ports
// are forwarded here: dockerd inside the guest already does hostPort ->
// containerPort itself, listening on the guest's own interface.
const DeviceIP = deviceIP

// Network is the host-process side of the VM's network device.
type Network struct {
	vn *virtualnetwork.VirtualNetwork
}

// New builds the virtual network. The subnet, gateway and guest address are
// fixed so DeviceIP is known before the guest boots: the DHCP pool gives the
// one guest its first free address (TestDHCPOffer pins .2), and
// internal/portforward forwards to it.
func New() (*Network, error) {
	vn, err := virtualnetwork.New(&types.Configuration{
		Subnet:            subnet,
		GatewayIP:         gatewayIP,
		GatewayMacAddress: vmtypes.GatewayMAC,
		Protocol:          types.VfkitProtocol,
		// No default: with 0 every gVisor UDP write fails, so DHCP never
		// answers. gvproxy sets 1500 itself.
		MTU:    1500,
		HostIP: HostIP,
		// Lets the gateway answer ARP for HostIP too, so traffic the
		// guest sends to it is delivered to the NAT/forwarder below
		// instead of going unanswered.
		GatewayVirtualIPs: []string{HostIP},
		NAT: map[string]string{
			HostIP: "127.0.0.1",
		},
		// DNS queries the gateway can't answer locally are resolved
		// through net.Resolver{PreferGo: false}, which on macOS is the
		// system resolver: /etc/hosts and VPN split DNS work from the
		// guest exactly as they do on the Mac.
		DNS: []types.Zone{
			{
				Name:      hostDNSZone,
				Protected: true,
				Records: []types.Record{
					{Name: "host", IP: net.ParseIP(HostIP)},
					{Name: "gateway", IP: net.ParseIP(gatewayIP)},
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("netstack: %w", err)
	}
	return &Network{vn: vn}, nil
}

// Expose forwards localAddr ("<hostIP>:<port>", the host IP and port
// dockerd requested) on the Mac to remoteAddr in the virtual network. A bind
// conflict on the Mac (e.g. another process already holds the port) comes
// back as an error.
func (n *Network) Expose(protocol, localAddr, remoteAddr string) error {
	return n.forwarderCall("/services/forwarder/expose", types.ExposeRequest{
		Local:    localAddr,
		Remote:   remoteAddr,
		Protocol: types.TransportProtocol(protocol),
	})
}

// Unexpose removes a forward added by Expose.
func (n *Network) Unexpose(protocol, localAddr string) error {
	return n.forwarderCall("/services/forwarder/unexpose", types.UnexposeRequest{
		Local:    localAddr,
		Protocol: types.TransportProtocol(protocol),
	})
}

// forwarderCall drives gvisor-tap-vsock's forwarder through its own HTTP
// API, in-process: the forwarder has no Go-level Expose/Unexpose exported
// on VirtualNetwork, only this mux (GatewayMux, the same one the guest
// could reach over the virtual network).
func (n *Network) forwarderCall(path string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("netstack: %w", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	rec := httptest.NewRecorder()
	n.vn.GatewayMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return fmt.Errorf("netstack: %s: %s", path, strings.TrimSpace(rec.Body.String()))
	}
	return nil
}

// Socketpair is the VM's NIC: it creates a SOCK_DGRAM pair and hands the
// gateway end to gvisor-tap-vsock in a goroutine; the other end's fd is
// returned for the VM process (fd 3, krun_add_net_unixgram with
// NET_FLAG_VFKIT). Called once per VM boot by internal/serve.
func (n *Network) Socketpair(ctx context.Context) (vmFD int, closeGateway func() error, err error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		return 0, nil, fmt.Errorf("netstack: socketpair: %w", err)
	}
	gatewayFD, childFD := fds[0], fds[1]
	f := os.NewFile(uintptr(gatewayFD), "vfkit-gateway")
	gwConn, err := net.FileConn(f)
	if err != nil {
		f.Close()
		unix.Close(childFD)
		return 0, nil, fmt.Errorf("netstack: FileConn: %w", err)
	}
	f.Close() // net.FileConn dup'd the fd; gwConn owns its own now.
	gatewayConn := gwConn
	go func() {
		if err := n.vn.AcceptVfkit(ctx, gatewayConn); err != nil {
			// Normal on shutdown: the VM process exiting closes its end.
		}
	}()
	return childFD, gatewayConn.Close, nil
}
