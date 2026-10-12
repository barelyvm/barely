//go:build linux

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"
)

// Disk order: vda is the read-only root
// image, vdb is the sparse data disk the host re-extends before every boot.
const (
	rootDisk = "/dev/vda"
	dataDisk = "/dev/vdb"
	dataDir  = "/var/lib/docker"
)

// bootstrap runs once, as PID 1, alongside the agent's servers. It mounts
// the read-only root image under a writable tmpfs overlay, moves into it,
// brings up the data disk and networking, and starts dockerd. There's no
// initramfs handoff: this binary is both /init and the long-running agent,
// so after chrooting it just keeps executing in place.
func bootstrap() error {
	log.Print("bootstrap starting")
	// The initramfs environment is empty; Alpine keeps its binaries under
	// /sbin and /usr/sbin as well as /bin and /usr/bin.
	os.Setenv("PATH", "/sbin:/usr/sbin:/bin:/usr/bin")

	log.Print("mounting root overlay")
	if err := mountOverlayRoot(); err != nil {
		return fmt.Errorf("overlay root: %w", err)
	}
	if err := mountDevPts(); err != nil {
		log.Printf("devpts: %v (continuing; barely shell's PTY sessions will fail)", err)
	}
	if err := os.MkdirAll("/sys/fs/cgroup", 0o755); err != nil {
		return fmt.Errorf("mkdir cgroup: %w", err)
	}
	if err := unix.Mount("cgroup2", "/sys/fs/cgroup", "cgroup2", 0, ""); err != nil {
		return fmt.Errorf("mount cgroup2: %w", err)
	}
	log.Print("mounting data disk")
	if err := mountDataDisk(); err != nil {
		return fmt.Errorf("data disk: %w", err)
	}
	args, err := bootArgs()
	if err != nil {
		return fmt.Errorf("boot args: %w", err)
	}
	log.Print("mounting shared roots")
	if err := mountShares(args.Shares); err != nil {
		log.Printf("shares: %v (continuing; bind mounts from the Mac will be missing)", err)
	}
	// Before dockerd, so no container can start without the amd64 stub in place.
	registerAmd64Stub()
	log.Print("bringing up network")
	if err := bringUpNetwork(); err != nil {
		log.Printf("network: %v (continuing; docker pulls will fail)", err)
	} else {
		log.Print("network up")
	}
	log.Print("starting dockerd")
	if err := startDockerd(); err != nil {
		return fmt.Errorf("dockerd: %w", err)
	}
	go func() {
		waitDockerReady()
		aliasDockerSocket()
		log.Print("dockerd ready")
		close(dockerReady)
	}()
	return nil
}

// aliasDockerSocket bind-mounts dockerd's socket over the Mac-side paths of
// barely's socket that the guest sees through virtio-fs (guestargs
// DockerSockAliases), so containers that mount those paths get a socket
// they can connect to. Only the guest's view changes; nothing is written
// to the Mac.
func aliasDockerSocket() {
	args, err := bootArgs()
	if err != nil {
		return
	}
	for _, p := range args.DockerSockAliases {
		if _, err := os.Lstat(p); err != nil {
			continue
		}
		if err := unix.Mount("/run/docker.sock", p, "", unix.MS_BIND, ""); err != nil {
			log.Printf("docker socket alias %s: %v", p, err)
		}
	}
}

// mountKernelFS mounts /dev, /proc and /sys. It runs before the agent's
// vsock listeners, which need /dev/vsock, and before the rest of bootstrap.
func setHostname() {
	if err := unix.Sethostname([]byte("barely")); err != nil {
		log.Printf("bootstrap: sethostname: %v", err)
	}
}

func mountKernelFS() error {
	for _, m := range []struct{ source, target, fstype string }{
		{"devtmpfs", "/dev", "devtmpfs"},
		{"proc", "/proc", "proc"},
		{"sysfs", "/sys", "sysfs"},
	} {
		if err := os.MkdirAll(m.target, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", m.target, err)
		}
		// /dev is often already mounted by the kernel itself
		// (CONFIG_DEVTMPFS_MOUNT); EBUSY there just means it's done.
		if err := unix.Mount(m.source, m.target, m.fstype, 0, ""); err != nil && err != unix.EBUSY {
			return fmt.Errorf("mount %s: %w", m.target, err)
		}
	}
	return nil
}

// mountOverlayRoot mounts the read-only erofs root image under a tmpfs
// overlay so the rest of boot (and dockerd) sees an ordinary writable root,
// then moves into it. Nothing here needs an initramfs or switch_root: this
// process's own text is already mapped, so it keeps running after chroot.
func mountOverlayRoot() error {
	for _, dir := range []string{"/mnt/root", "/mnt/rw", "/mnt/rw/upper", "/mnt/rw/work", "/newroot"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := unix.Mount(rootDisk, "/mnt/root", "erofs", unix.MS_RDONLY, ""); err != nil {
		return fmt.Errorf("mount root image: %w", err)
	}
	if err := unix.Mount("tmpfs", "/mnt/rw", "tmpfs", 0, ""); err != nil {
		return fmt.Errorf("mount tmpfs: %w", err)
	}
	if err := os.MkdirAll("/mnt/rw/upper", 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll("/mnt/rw/work", 0o755); err != nil {
		return err
	}
	opts := "lowerdir=/mnt/root,upperdir=/mnt/rw/upper,workdir=/mnt/rw/work"
	if err := unix.Mount("overlay", "/newroot", "overlay", 0, opts); err != nil {
		return fmt.Errorf("mount overlay: %w", err)
	}
	for _, dir := range []string{"dev", "proc", "sys"} {
		if err := unix.Mount("/"+dir, "/newroot/"+dir, "", unix.MS_MOVE, ""); err != nil {
			return fmt.Errorf("move %s: %w", dir, err)
		}
	}
	if err := unix.Chdir("/newroot"); err != nil {
		return err
	}
	if err := unix.Mount("/newroot", "/", "", unix.MS_MOVE, ""); err != nil {
		return fmt.Errorf("move newroot: %w", err)
	}
	if err := unix.Chroot("."); err != nil {
		return err
	}
	return unix.Chdir("/")
}

// mountDevPts mounts devpts at /dev/pts and symlinks /dev/ptmx to it, so
// opening /dev/ptmx (the standard path every PTY library, including
// creack/pty, uses) actually allocates a new pseudo-terminal — needed for
// "barely shell"'s interactive sessions.
// devtmpfs alone doesn't create a working /dev/ptmx.
func mountDevPts() error {
	if err := os.MkdirAll("/dev/pts", 0o755); err != nil {
		return err
	}
	if err := unix.Mount("devpts", "/dev/pts", "devpts", 0, "newinstance,ptmxmode=0666,mode=0620"); err != nil {
		return fmt.Errorf("mount devpts: %w", err)
	}
	os.Remove("/dev/ptmx")
	return os.Symlink("pts/ptmx", "/dev/ptmx")
}

// mountDataDisk mounts vdb at /var/lib/docker, formatting it first if it has
// no filesystem yet (first boot, or a disk the host just re-extended past a
// truncate-on-discard). No "discard" mount option: trims are explicit and
// lazy, triggered by the host over the control channel.
func mountDataDisk() error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	err := unix.Mount(dataDisk, dataDir, "ext4", 0, "")
	if err == nil {
		growDataDisk()
		return nil
	}
	// -E nodiscard: mke2fs's default format pass discards the whole device,
	// which hits libkrun's truncate-on-discard bug on the last range and
	// shrinks data.img before it's ever been used. No discard at all until
	// the explicit, lazy fstrim.
	//
	// This doesn't fully close the bug: even with nodiscard, a fresh format
	// still loses 64 KiB off the tail (measured; down from 320 KiB without
	// nodiscard), from something other than mke2fs's own discard pass —
	// tried lazy_itable_init=0/lazy_journal_init=0 too (mke2fs otherwise
	// defers zeroing those to a post-mount kernel thread via BLKZEROOUT,
	// another path to the same bug) and it made no difference on a truly
	// fresh disk, so it's left out here rather than paying for eager
	// zeroing on a 100 GiB DiskMax for nothing. The rest is libkrun's
	// truncate-on-discard bug itself (build-host) and
	// the host's before-every-boot re-extend.
	if out, mkErr := exec.Command("mkfs.ext4", "-F", "-q", "-E", "nodiscard", dataDisk).CombinedOutput(); mkErr != nil {
		return fmt.Errorf("mkfs.ext4: %w: %s", mkErr, out)
	}
	if err := unix.Mount(dataDisk, dataDir, "ext4", 0, ""); err != nil {
		return fmt.Errorf("mount data disk: %w", err)
	}
	return nil
}

// growDataDisk grows the mounted filesystem to fill vdb, for a disk_max
// raised since the disk was formatted (the host has already extended
// data.img). resize2fs does nothing when the sizes already match, and a
// failure leaves the disk usable at its old size, so it never fails boot.
func growDataDisk() {
	if out, err := exec.Command("resize2fs", dataDisk).CombinedOutput(); err != nil {
		log.Printf("bootstrap: resize2fs %s: %v: %s", dataDisk, err, out)
	}
}

// bringUpNetwork brings up loopback and DHCPs eth0. gvisor-tap-vsock on the
// host answers both the DHCP request and DNS.
func bringUpNetwork() error {
	if out, err := exec.Command("ip", "link", "set", "lo", "up").CombinedOutput(); err != nil {
		return fmt.Errorf("lo up: %w: %s", err, out)
	}
	// eth0 has to be administratively up before udhcpc will even send a
	// discover: without this it was hanging boot indefinitely instead of
	// failing after its own -t/-T retry budget.
	if out, err := exec.Command("ip", "link", "set", "eth0", "up").CombinedOutput(); err != nil {
		return fmt.Errorf("eth0 up: %w: %s", err, out)
	}
	// Belt and suspenders: a hard timeout in addition to udhcpc's own
	// retry budget, so a DHCP server that never answers can never hang
	// the rest of boot.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "udhcpc", "-i", "eth0", "-q", "-n", "-t", "5", "-T", "1").CombinedOutput(); err != nil {
		return fmt.Errorf("udhcpc: %w: %s", err, out)
	}
	return nil
}

// dockerdCmd is the running dockerd child; dockerdDone closes once the one
// goroutine that calls its Wait closes it. stopDockerd (control.go, on
// "shutdown") sends SIGTERM and waits on dockerdDone rather than calling
// Wait a second time.
var (
	dockerdCmd  *exec.Cmd
	dockerdDone = make(chan struct{})
)

func startDockerd() error {
	cmd := exec.Command("dockerd", "--host=unix:///run/docker.sock", "--pidfile=/run/dockerd.pid")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	dockerdCmd = cmd
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("dockerd exited: %v", err)
		}
		close(dockerdDone)
	}()
	return nil
}

// waitDockerReady polls for dockerd answering on its socket, used to send
// the control channel's unsolicited "ready" message.
func waitDockerReady() {
	for {
		conn, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
		if err == nil {
			sa := &unix.SockaddrUnix{Name: "/run/docker.sock"}
			cerr := unix.Connect(conn, sa)
			unix.Close(conn)
			if cerr == nil {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// stopDockerd stops dockerd cleanly and syncs, for the control channel's
// "shutdown".
func stopDockerd() {
	if dockerdCmd != nil && dockerdCmd.Process != nil {
		_ = dockerdCmd.Process.Signal(unix.SIGTERM)
		select {
		case <-dockerdDone:
		case <-time.After(10 * time.Second):
			_ = dockerdCmd.Process.Kill()
		}
	}
	unix.Sync()
}

// unmountDataDisk leaves ext4 on the data disk marked clean, so the next boot
// mounts it without replaying the journal. A mount something still holds is
// remounted read-only, which also leaves it clean.
func unmountDataDisk() {
	err := unix.Unmount(dataDir, 0)
	if err == nil || err == unix.EINVAL {
		return
	}
	if roErr := unix.Mount("", dataDir, "", unix.MS_REMOUNT|unix.MS_RDONLY, ""); roErr != nil {
		log.Printf("data disk: unmount: %v; remount read-only: %v", err, roErr)
	}
}

// powerOff powers the guest off. libkrun's shutdown path
// expects the VM to exit on its own once the agent replies to "shutdown".
func powerOff() {
	unmountDataDisk()
	unix.Sync()
	_ = unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF)
}
