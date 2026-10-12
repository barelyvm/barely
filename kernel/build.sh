#!/bin/sh
# Runs inside the build container. /kernel is this directory (read-only except
# out/), /build is a Docker volume holding the tarball, trees and objects so
# nothing large lands on the Mac's disk.
set -eu

. /kernel/version.env
mode="${1:-image}"

tarball="/build/linux-${KERNEL_VERSION}.tar.xz"
pristine="/build/linux-${KERNEL_VERSION}"
src=/build/src
obj=/build/obj

if [ ! -f "$tarball" ]; then
	curl -fsSL -o "$tarball.part" "$KERNEL_URL"
	mv "$tarball.part" "$tarball"
fi
echo "${KERNEL_SHA256}  $tarball" | sha256sum -c -

if [ ! -d "$pristine" ]; then
	rm -rf "$pristine.part"
	mkdir "$pristine.part"
	tar -xf "$tarball" -C "$pristine.part" --strip-components=1
	mv "$pristine.part" "$pristine"
fi

# Untouched files are hard links to the pristine tree with its mtimes, so the
# work tree costs no extra disk and only patched files rebuild. patch(1)
# replaces files rather than writing through the links.
rsync -a --delete --link-dest="$pristine/" "$pristine/" "$src/"
for p in /kernel/patches/*.patch; do
	[ -e "$p" ] || continue
	echo "applying $(basename "$p")"
	patch -d "$src" -p1 --no-backup-if-mismatch -s < "$p"
done

mkdir -p "$obj"
jobs="$(nproc)"

case "$mode" in
config)
	make -C "$src" O="$obj" ARCH=arm64 defconfig
	"$src/scripts/kconfig/merge_config.sh" -m -O "$obj" "$obj/.config" /kernel/config.fragment
	make -C "$src" O="$obj" ARCH=arm64 olddefconfig
	# Report fragment options that didn't survive (missing deps or renamed).
	grep -E '^(# )?CONFIG_' /kernel/config.fragment | while read -r line; do
		case "$line" in
		"# CONFIG_"*) opt="${line#\# }"; opt="${opt%% *}"; want="# $opt is not set" ;;
		*) opt="${line%%=*}"; want="$line" ;;
		esac
		got="$(grep -E "^(# )?$opt[= ]" "$obj/.config" || true)"
		if [ "$got" != "$want" ] && ! { [ -z "$got" ] && [ "${want#\# }" != "$want" ]; }; then
			echo "config: wanted '$want', got '${got:-unset}'"
		fi
	done
	cp "$obj/.config" /kernel/config-arm64
	;;
image)
	cp /kernel/config-arm64 "$obj/.config"
	make -C "$src" O="$obj" ARCH=arm64 olddefconfig
	if ! cmp -s /kernel/config-arm64 "$obj/.config"; then
		echo "config-arm64 is not olddefconfig-clean; run 'make kernel-config'" >&2
		diff /kernel/config-arm64 "$obj/.config" >&2 || true
		exit 1
	fi
	make -C "$src" O="$obj" ARCH=arm64 -j"$jobs" Image
	cp "$obj/arch/arm64/boot/Image" /kernel/out/Image
	cp "$obj/.config" /kernel/out/config
	echo "$KERNEL_VERSION" > /kernel/out/version
	ls -l /kernel/out/Image
	;;
*)
	echo "unknown mode $mode" >&2
	exit 2
	;;
esac
