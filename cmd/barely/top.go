package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"barely/internal/footprint"
	"barely/internal/serve"
)

// runTop implements "barely top" (GET /top?interval=1s):
// a live view refreshed every second of the
// VM's memory footprint with a sparkline history, guest used/cache,
// per-container memory/CPU, and disk.
func runTop(args []string) error {
	paths, err := cliPaths()
	if err != nil {
		return err
	}
	client := controlHTTPNoTimeout(paths)
	var history []footprint.Sample
	for {
		resp, err := client.Get("http://barely/top?interval=1s")
		if err != nil {
			renderNotRunning()
			time.Sleep(2 * time.Second)
			continue
		}
		sc := bufio.NewScanner(resp.Body)
		// The first event carries the whole footprint history, which outgrows
		// bufio's default 64 KiB line limit.
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if !bytes.HasPrefix([]byte(line), []byte("data: ")) {
				continue
			}
			var ev serve.TopEvent
			if err := json.Unmarshal([]byte(line[len("data: "):]), &ev); err != nil {
				continue
			}
			if len(ev.History) > 0 {
				// Only the first event of a stream has History; later events
				// extend it locally.
				history = ev.History
			} else if ev.State == "running" {
				history = append(history, footprint.Sample{At: time.Now(), Bytes: ev.VMFootprint})
				// 120 samples (two minutes at 1s) keeps the sparkline within one
				// terminal row.
				if len(history) > 120 {
					history = history[len(history)-120:]
				}
			}
			renderTop(ev, history)
		}
		resp.Body.Close()
	}
}

func renderNotRunning() {
	fmt.Print("\033[H\033[2J")
	fmt.Printf("barely top — %s\n\n", time.Now().Format("15:04:05"))
	fmt.Println("barely isn't running — it starts on the next docker command (or `barely start`).")
	fmt.Println("Waiting…")
}

func renderTop(ev serve.TopEvent, history []footprint.Sample) {
	fmt.Print("\033[H\033[2J") // clear screen, home cursor
	fmt.Printf("barely top — %s\n\n", time.Now().Format("15:04:05"))
	switch ev.State {
	case "booting":
		fmt.Println("VM booting…")
		return
	case "running":
	default:
		fmt.Println("VM stopped — it starts on the next docker command (or `barely start`).")
		if ev.VMFootprintPeak.Bytes > 0 {
			fmt.Printf("Last run peaked at %s.\n", humanBytes(ev.VMFootprintPeak.Bytes))
		}
		fmt.Printf("disk:       %s logical, %s on disk\n", humanBytes(ev.Disk.Logical), humanBytes(ev.Disk.OnDisk))
		return
	}
	fmt.Printf("VM memory:  %s now, peak %s", humanBytes(ev.VMFootprint), humanBytes(ev.VMFootprintPeak.Bytes))
	if !ev.VMFootprintPeak.At.IsZero() {
		fmt.Printf(" %s ago", time.Since(ev.VMFootprintPeak.At).Round(time.Second))
	}
	fmt.Println()
	fmt.Printf("            %s\n", sparkline(history))
	if ev.DockerDesktopFootprint != nil {
		fmt.Printf("docker desktop vm: %s\n", humanBytes(*ev.DockerDesktopFootprint))
	}
	fmt.Printf("guest:      used %s, cache %s\n", humanBytes(ev.Guest.TotalBytes-ev.Guest.AvailableBytes), humanBytes(ev.Guest.CacheBytes))
	fmt.Printf("disk:       %s logical, %s on disk\n", humanBytes(ev.Disk.Logical), humanBytes(ev.Disk.OnDisk))
	fmt.Println()
	if len(ev.Containers) == 0 {
		fmt.Println("no running containers")
		return
	}
	sort.Slice(ev.Containers, func(i, j int) bool { return ev.Containers[i].Mem > ev.Containers[j].Mem })
	fmt.Printf("%-30s %12s %8s\n", "CONTAINER", "MEM", "CPU%")
	for _, c := range ev.Containers {
		fmt.Printf("%-30s %12s %7.1f%%\n", truncate(c.Name, 30), humanBytes(c.Mem), c.CPU)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// sparkline renders samples as a one-line bar chart using the 8 Unicode
// block-height characters, scaled to the series' own min/max.
func sparkline(samples []footprint.Sample) string {
	if len(samples) == 0 {
		return ""
	}
	blocks := []rune(" ▁▂▃▄▅▆▇█")
	min, max := samples[0].Bytes, samples[0].Bytes
	for _, s := range samples {
		if s.Bytes < min {
			min = s.Bytes
		}
		if s.Bytes > max {
			max = s.Bytes
		}
	}
	out := make([]rune, 0, len(samples))
	for _, s := range samples {
		if max == min {
			out = append(out, blocks[0])
			continue
		}
		idx := int(float64(s.Bytes-min) / float64(max-min) * float64(len(blocks)-1))
		out = append(out, blocks[idx])
	}
	return string(out)
}
