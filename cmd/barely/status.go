package main

import (
	"fmt"
	"time"
)

// runStatus implements "barely status" (GET /status): the VM's memory
// footprint now against its recent peak, then guest memory and disk. It
// reports "not running" instead of failing when there is no host process.
func runStatus(args []string) error {
	paths, err := cliPaths()
	if err != nil {
		return err
	}
	st, err := fetchStatus(paths)
	if err != nil {
		return err
	}

	switch st.State {
	case stateNotRunning:
		fmt.Println("barely isn't running — it starts on the next docker command (or `barely start`)")
		return nil
	case "booting":
		fmt.Println("state:  booting")
		return nil
	case "running":
		// Falls out of the switch to the full report below.
	default:
		fmt.Printf("state:  %s (the host process is up; the VM boots on the next docker command)\n", st.State)
		return nil
	}
	fmt.Printf("state:  %s\n", st.State)
	fmt.Printf("uptime: %s\n", st.Uptime.Round(time.Second))
	fmt.Printf("memory: %s now, peak %s", humanBytes(st.VMFootprint), humanBytes(st.VMFootprintPeak.Bytes))
	if !st.VMFootprintPeak.At.IsZero() {
		fmt.Printf(" %s ago", time.Since(st.VMFootprintPeak.At).Round(time.Second))
	}
	fmt.Println()
	if st.DockerDesktopFootprint != nil {
		fmt.Printf("docker desktop vm: %s\n", humanBytes(*st.DockerDesktopFootprint))
	}
	fmt.Printf("guest:  used %s, cache %s\n",
		humanBytes(st.Guest.TotalBytes-st.Guest.AvailableBytes), humanBytes(st.Guest.CacheBytes))
	fmt.Printf("disk:   %s logical, %s on disk\n", humanBytes(st.Disk.Logical), humanBytes(st.Disk.OnDisk))
	if st.IdleIn > 0 {
		fmt.Printf("idle in: %s\n", st.IdleIn.Round(time.Second))
	}
	return nil
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := "KMGTPE"
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), units[exp])
}
