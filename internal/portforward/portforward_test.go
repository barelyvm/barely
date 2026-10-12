// The suite protects the container-event to Expose/Unexpose bookkeeping
// against a fake Exposer and a fake dockerd on a unix socket; no VM or
// network is needed. Run with go test ./internal/portforward.
package portforward

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// fakeExposer records Expose/Unexpose calls and can be told to fail a
// given local address, like a real bind conflict on the Mac.
type fakeExposer struct {
	exposed map[string]string // "protocol local" -> remote
	fail    map[string]bool
}

func newFakeExposer() *fakeExposer {
	return &fakeExposer{exposed: map[string]string{}, fail: map[string]bool{}}
}

func (f *fakeExposer) Expose(protocol, local, remote string) error {
	key := protocol + " " + local
	if f.fail[key] {
		return fmt.Errorf("bind: address already in use")
	}
	f.exposed[key] = remote
	return nil
}

func (f *fakeExposer) Unexpose(protocol, local string) error {
	delete(f.exposed, protocol+" "+local)
	return nil
}

// TestApplyExposesAndReplacesBindings pins that a container's published
// ports are swapped, not accumulated, when dockerd republishes them.
func TestApplyExposesAndReplacesBindings(t *testing.T) {
	exp := newFakeExposer()
	w := New("", "192.168.127.2", exp)

	w.apply("ctr1", []binding{{Protocol: "tcp", HostIP: "0.0.0.0", Port: "8080"}})
	if got, want := exp.exposed["tcp 0.0.0.0:8080"], "192.168.127.2:8080"; got != want {
		t.Fatalf("exposed[%q] = %q, want %q", "tcp 0.0.0.0:8080", got, want)
	}

	// A second apply for the same container with a different port drops
	// the old one, matching dockerd republishing on restart.
	w.apply("ctr1", []binding{{Protocol: "tcp", HostIP: "0.0.0.0", Port: "9090"}})
	if _, ok := exp.exposed["tcp 0.0.0.0:8080"]; ok {
		t.Fatal("old binding 8080 still exposed")
	}
	if _, ok := exp.exposed["tcp 0.0.0.0:9090"]; !ok {
		t.Fatal("new binding 9090 not exposed")
	}
}

// TestReleaseUnexposesAllBindings pins that a stopped container leaves no
// forward behind on the Mac.
func TestReleaseUnexposesAllBindings(t *testing.T) {
	exp := newFakeExposer()
	w := New("", "192.168.127.2", exp)

	w.apply("ctr1", []binding{
		{Protocol: "tcp", HostIP: "0.0.0.0", Port: "8080"},
		{Protocol: "udp", HostIP: "127.0.0.1", Port: "53"},
	})
	w.release("ctr1")
	if len(exp.exposed) != 0 {
		t.Fatalf("exposed = %v, want empty after release", exp.exposed)
	}
}

// TestCollapseDualStackMergesWildcardAndLoopbackPairs pins the dedupe that
// avoids a phantom "already in use" on the IPv6 half of every published port.
func TestCollapseDualStackMergesWildcardAndLoopbackPairs(t *testing.T) {
	in := []binding{
		{Protocol: "tcp", HostIP: "0.0.0.0", Port: "8080"},
		{Protocol: "tcp", HostIP: "::", Port: "8080"},
		{Protocol: "tcp", HostIP: "127.0.0.1", Port: "2222"},
		{Protocol: "tcp", HostIP: "::1", Port: "2222"},
		{Protocol: "tcp", HostIP: "192.168.1.5", Port: "9090"}, // explicit interface: untouched
	}
	got := collapseDualStack(in)

	want := map[string]bool{
		"tcp 0.0.0.0:8080":     true,
		"tcp 127.0.0.1:2222":   true,
		"tcp 192.168.1.5:9090": true,
	}
	if len(got) != len(want) {
		t.Fatalf("collapseDualStack(%v) = %v, want %d bindings", in, got, len(want))
	}
	for _, b := range got {
		key := b.Protocol + " " + b.local()
		if !want[key] {
			t.Errorf("unexpected binding %q in collapsed result %v", key, got)
		}
	}
}

// TestApplyExposesWildcardPairOnce pins the dedupe end to end: one Expose
// per dual-stack pair.
func TestApplyExposesWildcardPairOnce(t *testing.T) {
	exp := newFakeExposer()
	w := New("", "192.168.127.2", exp)

	// What a real nginx -p 8080:80 inspect reports: one docker-proxy
	// listener, described as both address families.
	w.apply("ctr1", collapseDualStack([]binding{
		{Protocol: "tcp", HostIP: "0.0.0.0", Port: "8080"},
		{Protocol: "tcp", HostIP: "::", Port: "8080"},
	}))

	if len(exp.exposed) != 1 {
		t.Fatalf("exposed = %v, want exactly one Expose call for a dual-stack publish", exp.exposed)
	}
}

// TestApplyRetriesFailedBind pins that a refused bind exposes nothing and does
// not stop a later apply from exposing the same address once it is free.
func TestApplyRetriesFailedBind(t *testing.T) {
	exp := newFakeExposer()
	exp.fail["tcp 0.0.0.0:8080"] = true
	w := New("", "192.168.127.2", exp)

	w.apply("ctr1", []binding{{Protocol: "tcp", HostIP: "0.0.0.0", Port: "8080"}})
	if len(exp.exposed) != 0 {
		t.Fatalf("exposed = %v, want nothing exposed for a failed bind", exp.exposed)
	}

	exp.fail["tcp 0.0.0.0:8080"] = false
	w.apply("ctr1", []binding{{Protocol: "tcp", HostIP: "0.0.0.0", Port: "8080"}})
	if len(exp.exposed) != 1 {
		t.Fatalf("exposed = %v, want the bind exposed once the port is free", exp.exposed)
	}
}

// fakeDockerd serves the two endpoints the watcher calls over a Unix
// socket, standing in for the guest's dockerd.
func fakeDockerd(t *testing.T, mux *http.ServeMux) string {
	t.Helper()
	// Short, not under t.TempDir(): nested test names easily blow the
	// 104-byte Unix socket path limit (also noted in internal/serve).
	dir, err := os.MkdirTemp("", "barely-pf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sockPath := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	t.Cleanup(func() {
		srv.Close()
		os.Remove(sockPath)
	})
	return sockPath
}

// TestReconcileExposesRunningContainersAndDropsStale pins the catch-up after
// an events-stream reconnect: running containers are exposed, containers
// that died meanwhile are released, and EXPOSEd-only ports are ignored.
func TestReconcileExposesRunningContainersAndDropsStale(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/containers/json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"Id":"abc123","Ports":[{"IP":"0.0.0.0","PrivatePort":80,"PublicPort":8080,"Type":"tcp"},{"IP":"","PrivatePort":443,"PublicPort":0,"Type":"tcp"}]}]`)
	})
	sockPath := fakeDockerd(t, mux)

	exp := newFakeExposer()
	w := New(sockPath, "192.168.127.2", exp)

	// A stale entry from a container dockerd no longer reports (e.g. it
	// was removed while the watcher was disconnected).
	w.byCtr["gone"] = []binding{{Protocol: "tcp", HostIP: "0.0.0.0", Port: "1234"}}
	exp.exposed["tcp 0.0.0.0:1234"] = "192.168.127.2:1234"

	if err := w.reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}

	if got, want := exp.exposed["tcp 0.0.0.0:8080"], "192.168.127.2:8080"; got != want {
		t.Fatalf("exposed[%q] = %q, want %q", "tcp 0.0.0.0:8080", got, want)
	}
	if _, ok := exp.exposed["tcp 0.0.0.0:1234"]; ok {
		t.Fatal("stale binding for a container no longer reported by dockerd is still exposed")
	}
	// PublicPort 0 means EXPOSEd, not published: must not be forwarded.
	for key := range exp.exposed {
		if key == "tcp 0.0.0.0:443" || key == "tcp :443" {
			t.Fatalf("unpublished port 443 was exposed: %v", exp.exposed)
		}
	}
}
