//go:build linux

package main

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"barely/internal/proto"
	"barely/internal/vmtypes"
)

// Proactive reclaim: free page reporting hands
// back only memory the guest has freed, and Linux keeps file cache around
// indefinitely, so after a build the VM would sit at its cap. Once the
// cache stops growing, reclaim it down to a small floor; the freed pages
// are then reported and libkrun returns them to macOS.
const (
	reclaimInterval = 5 * time.Second
	// settleWindow is how long the cache must stop growing before it's
	// treated as cold: reclaiming mid-build would only make the build
	// re-read what it just cached.
	settleWindow = 30 * time.Second
	settleGrowth = 64 << 20
	cacheFloor   = 256 << 20
	reclaimStep  = 1 << 30
	// compactGain is how much free memory must have appeared since the
	// last compaction before compacting again. A workload that made many
	// small allocations leaves its freed memory fragmented, and only
	// contiguous free blocks are reported to the host.
	compactGain = 256 << 20
	// psiBusy is the memory pressure (some avg10, %) above which reclaim
	// waits: the guest is already short of memory and thrashing.
	psiBusy = 10.0
)

type memInfo struct {
	total, free, available, cache uint64
}

func readMemInfo() (memInfo, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return memInfo{}, err
	}
	defer f.Close()
	var m memInfo
	var cached, buffers uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		b := kb << 10
		switch fields[0] {
		case "MemTotal:":
			m.total = b
		case "MemFree:":
			m.free = b
		case "MemAvailable:":
			m.available = b
		case "Cached:":
			cached = b
		case "Buffers:":
			buffers = b
		}
	}
	m.cache = cached + buffers
	return m, sc.Err()
}

func readPSISomeAvg10() float64 {
	b, err := os.ReadFile("/proc/pressure/memory")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "some ") {
			continue
		}
		for _, f := range strings.Fields(line) {
			if v, ok := strings.CutPrefix(f, "avg10="); ok {
				x, _ := strconv.ParseFloat(v, 64)
				return x
			}
		}
	}
	return 0
}

func memStats() (vmtypes.MemStats, error) {
	m, err := readMemInfo()
	if err != nil {
		return vmtypes.MemStats{}, err
	}
	return vmtypes.MemStats{
		TotalBytes:     m.total,
		AvailableBytes: m.available,
		FreeBytes:      m.free,
		CacheBytes:     m.cache,
		PSISomeAvg10:   readPSISomeAvg10(),
	}, nil
}

// sendMemStats reports mem_stats every second on one control connection
// until a send fails (the host went away).
func sendMemStats(conn *controlConn) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		ms, err := memStats()
		if err != nil {
			log.Printf("memory: %v", err)
			continue
		}
		m, err := proto.New(proto.TypeMemStats, 0, ms)
		if err != nil {
			return
		}
		if err := conn.send(m); err != nil {
			return
		}
	}
}

type cacheSample struct {
	at    time.Time
	bytes uint64
}

// reclaimLoop runs for the life of the guest (PID 1 only).
func reclaimLoop() {
	var history []cacheSample
	var compactedAtFree uint64
	t := time.NewTicker(reclaimInterval)
	defer t.Stop()
	for range t.C {
		m, err := readMemInfo()
		if err != nil {
			continue
		}
		now := time.Now()
		history = append(history, cacheSample{now, m.cache})
		for len(history) > 0 && now.Sub(history[0].at) > settleWindow {
			history = history[1:]
		}
		if m.free < compactedAtFree {
			compactedAtFree = m.free
		}
		if !settled(history, now) || readPSISomeAvg10() > psiBusy {
			continue
		}
		if m.free > compactedAtFree+compactGain {
			if err := os.WriteFile("/proc/sys/vm/compact_memory", []byte("1"), 0); err != nil {
				log.Printf("memory: compact: %v", err)
			}
			compactedAtFree = m.free
		}
		if m.cache <= cacheFloor {
			continue
		}
		want := min(m.cache-cacheFloor, reclaimStep)
		if err := reclaim(want); err != nil {
			log.Printf("memory: reclaim %d MiB: %v", want>>20, err)
		}
	}
}

// settled reports whether the cache has stopped growing for a full
// settleWindow: the oldest sample in the window is old enough and the
// cache hasn't grown by more than settleGrowth since.
func settled(history []cacheSample, now time.Time) bool {
	if len(history) == 0 || now.Sub(history[0].at) < settleWindow-reclaimInterval {
		return false
	}
	first := history[0].bytes
	for _, s := range history {
		if s.bytes > first+settleGrowth {
			return false
		}
	}
	return true
}

// reclaim asks the kernel to reclaim n bytes from the root cgroup
// (cgroup v2 memory.reclaim). Partial reclaim reports EAGAIN, which is
// fine: whatever was freed is still freed.
func reclaim(n uint64) error {
	err := os.WriteFile("/sys/fs/cgroup/memory.reclaim", []byte(fmt.Sprintf("%d", n)), 0)
	if err == nil || errors.Is(err, syscall.EAGAIN) {
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.EINVAL) {
		return err
	}
	// Kernels without root memory.reclaim: drop clean page cache instead.
	return os.WriteFile("/proc/sys/vm/drop_caches", []byte("1"), 0)
}
