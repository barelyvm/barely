// Package portforward makes `docker run -p` work on the Mac: it follows
// dockerd's container events from the host process and keeps each published
// port exposed on the Mac while its container lives. The guest's dockerd
// already maps hostPort to containerPort inside the VM, so the Mac only
// needs a forward to the guest's address.
//
// Entry points are New and Watcher.Run; the forwarding itself is delegated to
// an Exposer (internal/netstack.Network).
//
// Used by: internal/serve, which runs one Watcher per VM boot.
package portforward

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// requestTimeout bounds the one-shot dockerd calls; the events stream
	// has no timeout because it is meant to stay open.
	requestTimeout = 10 * time.Second
	reconnectDelay = 2 * time.Second
)

// Exposer is the forwarding side of gvisor-tap-vsock (internal/netstack's
// *Network): expose/unexpose a host address on the virtual network.
type Exposer interface {
	Expose(protocol, localAddr, remoteAddr string) error
	Unexpose(protocol, localAddr string) error
}

type binding struct {
	Protocol string
	HostIP   string
	Port     string // same on both sides: dockerd already proxies hostPort -> containerPort inside the guest
}

func (b binding) local() string { return net.JoinHostPort(b.HostIP, b.Port) }

// collapseDualStack merges the 0.0.0.0/:: and 127.0.0.1/::1 pairs dockerd
// reports for a single published port into one binding each. Those are one
// docker-proxy listener covering both address families, not two separate
// requests; exposing both hit a phantom "already in use" the second time,
// for every published port. The IPv4 half is kept, since it's what
// localhost and plain docker/curl use resolve to on the Mac.
func collapseDualStack(bindings []binding) []binding {
	type key struct{ proto, port string }
	groups := make(map[key][]binding)
	var order []key
	for _, b := range bindings {
		k := key{b.Protocol, b.Port}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], b)
	}

	out := make([]binding, 0, len(bindings))
	for _, k := range order {
		out = append(out, collapsePair(groups[k])...)
	}
	return out
}

// collapsePair drops the IPv6 half of a wildcard or loopback pair within
// one protocol/port group. Any other host IP (an explicit interface
// dockerd was asked to bind to) passes through unchanged.
func collapsePair(group []binding) []binding {
	has := make(map[string]bool, len(group))
	for _, b := range group {
		has[b.HostIP] = true
	}
	drop := map[string]bool{}
	if has["0.0.0.0"] && has["::"] {
		drop["::"] = true
	}
	if has["127.0.0.1"] && has["::1"] {
		drop["::1"] = true
	}
	if len(drop) == 0 {
		return group
	}
	out := make([]binding, 0, len(group))
	for _, b := range group {
		if !drop[b.HostIP] {
			out = append(out, b)
		}
	}
	return out
}

// Watcher dials the guest's Docker socket (the passthrough path,
// v2375) as a plain HTTP client, subscribes to container events and
// reconciles published ports against them.
type Watcher struct {
	client   *http.Client
	exposer  Exposer
	deviceIP string

	mu        sync.Mutex
	byCtr     map[string][]binding
	conflicts map[string]string // "protocol local" -> last error, for status
}

// New dials v2375Path for each request; deviceIP is the guest's address on
// the virtual network (internal/netstack.DeviceIP), the forward target
// since dockerd's own port mapping already listens there.
func New(v2375Path, deviceIP string, exposer Exposer) *Watcher {
	return &Watcher{
		client: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", v2375Path)
				},
			},
		},
		exposer:   exposer,
		deviceIP:  deviceIP,
		byCtr:     make(map[string][]binding),
		conflicts: make(map[string]string),
	}
}

// Run watches until ctx is cancelled, reconnecting on any error: dockerd
// may not be listening yet when the VM has just booted, and a dropped
// stream would otherwise leave published ports stale.
func (w *Watcher) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := w.watchOnce(ctx); err != nil && ctx.Err() == nil {
			log.Printf("portforward: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(reconnectDelay):
		}
	}
}

// Conflicts returns a copy of the last-seen bind error per "protocol
// local". It has no production caller; only tests read it.
func (w *Watcher) Conflicts() map[string]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[string]string, len(w.conflicts))
	for k, v := range w.conflicts {
		out[k] = v
	}
	return out
}

// watchOnce runs one events-stream session. It reconciles first so
// containers that started or died while the stream was down (or before
// serve began watching) are picked up, then applies events until the stream
// breaks.
func (w *Watcher) watchOnce(ctx context.Context) error {
	if err := w.reconcile(ctx); err != nil {
		return fmt.Errorf("reconcile: %w", err)
	}

	filters := url.QueryEscape(`{"type":["container"]}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/events?filters="+filters, nil)
	if err != nil {
		return err
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("events: %s: %s", resp.Status, bytes.TrimSpace(body))
	}

	dec := json.NewDecoder(resp.Body)
	for {
		var ev struct {
			Type   string `json:"Type"`
			Action string `json:"Action"`
			Actor  struct {
				ID string `json:"ID"`
			} `json:"Actor"`
		}
		if err := dec.Decode(&ev); err != nil {
			return fmt.Errorf("events: %w", err)
		}
		if ev.Type != "container" {
			continue
		}
		switch ev.Action {
		case "start":
			w.onStart(ctx, ev.Actor.ID)
		case "die", "destroy":
			w.release(ev.Actor.ID)
		}
	}
}

// reconcile replaces every tracked container's bindings with what
// GET /containers/json currently reports.
func (w *Watcher) reconcile(ctx context.Context) error {
	rctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, "http://docker/containers/json", nil)
	if err != nil {
		return err
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(body))
	}

	var containers []struct {
		ID    string `json:"Id"`
		Ports []struct {
			IP         string `json:"IP"`
			PublicPort uint16 `json:"PublicPort"`
			Type       string `json:"Type"`
		} `json:"Ports"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return err
	}

	// Containers tracked but missing from the listing died while the events
	// stream was down; their forwards are released below.
	w.mu.Lock()
	stale := make(map[string]bool, len(w.byCtr))
	for id := range w.byCtr {
		stale[id] = true
	}
	w.mu.Unlock()

	for _, c := range containers {
		delete(stale, c.ID)
		var bindings []binding
		for _, p := range c.Ports {
			if p.PublicPort == 0 {
				continue // EXPOSEd but not published
			}
			hostIP := p.IP
			if hostIP == "" {
				hostIP = "0.0.0.0"
			}
			bindings = append(bindings, binding{Protocol: p.Type, HostIP: hostIP, Port: strconv.Itoa(int(p.PublicPort))})
		}
		w.apply(c.ID, collapseDualStack(bindings))
	}
	for id := range stale {
		w.release(id)
	}
	return nil
}

// onStart inspects a just-started container and applies its published
// ports.
func (w *Watcher) onStart(ctx context.Context, id string) {
	rctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, "http://docker/containers/"+id+"/json", nil)
	if err != nil {
		log.Printf("portforward: building inspect request for %s: %v", shortID(id), err)
		return
	}
	resp, err := w.client.Do(req)
	if err != nil {
		log.Printf("portforward: inspecting %s: %v", shortID(id), err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return // already stopped again by the time we asked
	}

	var c struct {
		NetworkSettings struct {
			Ports map[string][]struct {
				HostIP   string `json:"HostIp"`
				HostPort string `json:"HostPort"`
			} `json:"Ports"`
		} `json:"NetworkSettings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		log.Printf("portforward: decoding inspect of %s: %v", shortID(id), err)
		return
	}

	var bindings []binding
	for portProto, hostBindings := range c.NetworkSettings.Ports {
		_, proto, ok := strings.Cut(portProto, "/")
		if !ok {
			proto = "tcp"
		}
		for _, hb := range hostBindings {
			hostIP := hb.HostIP
			if hostIP == "" {
				hostIP = "0.0.0.0"
			}
			bindings = append(bindings, binding{Protocol: proto, HostIP: hostIP, Port: hb.HostPort})
		}
	}
	w.apply(id, collapseDualStack(bindings))
}

// apply replaces id's exposed bindings with want, exposing new ones and
// dropping ones no longer present.
func (w *Watcher) apply(id string, want []binding) {
	// Release first: a restarted container may republish on different ports,
	// and re-exposing an address this Watcher still holds would conflict
	// with itself.
	w.release(id)
	applied := make([]binding, 0, len(want))
	for _, b := range want {
		local := b.local()
		remote := net.JoinHostPort(w.deviceIP, b.Port)
		key := b.Protocol + " " + local
		if err := w.exposer.Expose(b.Protocol, local, remote); err != nil {
			log.Printf("portforward: exposing %s/%s for container %s: %v", local, b.Protocol, shortID(id), err)
			w.mu.Lock()
			w.conflicts[key] = err.Error()
			w.mu.Unlock()
			continue
		}
		w.mu.Lock()
		delete(w.conflicts, key)
		w.mu.Unlock()
		applied = append(applied, b)
	}
	if len(applied) == 0 {
		return
	}
	w.mu.Lock()
	w.byCtr[id] = applied
	w.mu.Unlock()
}

// release unexposes everything tracked for id.
func (w *Watcher) release(id string) {
	w.mu.Lock()
	bindings := w.byCtr[id]
	delete(w.byCtr, id)
	w.mu.Unlock()
	for _, b := range bindings {
		local := b.local()
		if err := w.exposer.Unexpose(b.Protocol, local); err != nil {
			log.Printf("portforward: unexposing %s/%s for container %s: %v", local, b.Protocol, shortID(id), err)
		}
		w.mu.Lock()
		delete(w.conflicts, b.Protocol+" "+local)
		w.mu.Unlock()
	}
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
