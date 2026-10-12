#!/bin/sh
# Runs inside the guest/Dockerfile builder. Builds root.img (erofs) and
# initrd.img from the staged agent binary and an Alpine userland, then
# writes manifest.json (GuestManifest). Everything lands
# in /out, bind-mounted to the host's out/guest/<version>/ (Makefile).
set -eu

version="$1"
kernel_version="$2"
agent_protocol="$3"
fsevent_abi="$4"

# Staged in the container, not under /build: package triggers open device
# nodes (/dev/null) in the rootfs, which a host bind mount can't provide.
rootfs=/rootfs
agent_bin=/build/agent/barely-agent
out=/out

rm -rf "$rootfs"
mkdir -p "$rootfs/etc/apk"
cp /etc/apk/repositories "$rootfs/etc/apk/repositories"
cp -r /etc/apk/keys "$rootfs/etc/apk/keys"
apk add --root "$rootfs" --no-cache --initdb \
	docker-engine docker-cli-buildx containerd runc \
	iptables ip6tables nftables ca-certificates e2fsprogs e2fsprogs-extra busybox

# Guest-only userland: no docker CLI, no docs/caches.
rm -rf "$rootfs"/usr/share/man "$rootfs"/usr/share/doc "$rootfs"/var/cache/apk
mkdir -p "$rootfs/var/lib/docker" "$rootfs/run" "$rootfs/proc" "$rootfs/sys" "$rootfs/dev" "$rootfs/mnt" "$rootfs/newroot"

# Handler registered for amd64 binaries so they fail with a clear message.
mkdir -p "$rootfs/usr/libexec/barely"
gcc -Os -static -s -o "$rootfs/usr/libexec/barely/amd64-unavailable" /guest/amd64-unavailable.c

mkdir -p "$out"
mkfs.erofs -zlz4hc,9 -T0 "$out/root.img" "$rootfs" >/dev/null

mkdir -p /build/initrd
cp "$agent_bin" /build/initrd/init
chmod 0755 /build/initrd/init
(cd /build/initrd && find . | cpio -o -H newc 2>/dev/null | gzip -9) > "$out/initrd.img"

sha256() { sha256sum "$1" | cut -d' ' -f1; }
cat >"$out/manifest.json" <<EOF
{
  "version": "$version",
  "kernel": "$kernel_version",
  "agent_protocol": $agent_protocol,
  "fsevent_abi": $fsevent_abi,
  "sha256": {
    "Image": "$(sha256 "$out/Image")",
    "initrd.img": "$(sha256 "$out/initrd.img")",
    "root.img": "$(sha256 "$out/root.img")"
  }
}
EOF
ls -l "$out/Image" "$out/initrd.img" "$out/root.img"
