module barely

go 1.27.1

require (
	github.com/containers/gvisor-tap-vsock v0.9.0
	github.com/creack/pty v1.1.24
	github.com/fsnotify/fsevents v0.2.0
	github.com/google/gopacket v1.1.19
	github.com/insomniacslk/dhcp v0.0.0-20240710054256-ddd8a41251c9
	github.com/mdlayher/vsock v1.3.0
	github.com/pelletier/go-toml/v2 v2.4.3
	golang.org/x/sys v0.48.0
	golang.org/x/term v0.46.0
)

require (
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/google/btree v1.1.2 // indirect
	github.com/inetaf/tcpproxy v0.0.0-20260515195445-c159a6051109 // indirect
	github.com/linuxkit/virtsock v0.0.0-20220523201153-1a23e78aa7a2 // indirect
	github.com/mdlayher/socket v0.6.0 // indirect
	github.com/miekg/dns v1.1.73 // indirect
	github.com/pierrec/lz4/v4 v4.1.14 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/sirupsen/logrus v1.10.2 // indirect
	github.com/u-root/uio v0.0.0-20240224005618-d2acac8f3701 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/exp v0.0.0-20231110203233-9a3e6036ecaa // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/time v0.12.0 // indirect
	gvisor.dev/gvisor v0.0.0-20260413194555-9680d69bf798 // indirect
)

replace github.com/containers/gvisor-tap-vsock => github.com/barelyvm/gvisor-tap-vsock v0.0.0-20261012103729-fcde58de5c8b
