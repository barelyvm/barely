#!/bin/zsh
# Reproduces the stop-then-immediate-docker sequence on a fresh disk and
# reports whether the data disk is corrupt on the following boot. Before
# commit 9f2f3f1 this corrupted the disk 3 times out of 3 (ext4 errors 8).
#
# Needs a throwaway instance at /tmp/barely-t first:
#   mkdir -p /tmp/barely-t/bin && cp out/bin/barely out/bin/barelyvm out/bin/libkrun.1.dylib /tmp/barely-t/bin/
#   cp -R ~/.barely/guest /tmp/barely-t/guest && printf 'disk_max = "2G"\n' > /tmp/barely-t/config.toml
# Usage: test-stop-race.sh <trials> <serve binary name in /tmp/barely-t/bin> <seconds between stop and docker>
# e.g.   test-stop-race.sh 5 barely 1
export BARELY_HOME=/tmp/barely-t
export DOCKER_HOST=unix:///tmp/barely-t/docker.sock
B=/tmp/barely-t/bin/barely
BIN=$2
DELAY=$3

serve_up() {
	(cd /tmp/barely-t && ./bin/$BIN serve -state-dir /tmp/barely-t >> /tmp/barely-t/serve.out 2>&1 &)
	sleep 2
}
serve_gone() {
	for i in $(seq 1 40); do
		if ! pgrep -f "state-dir /tmp/barely-t" > /dev/null; then
			return 0
		fi
		sleep 1
	done
	echo "  serve still running after 40s"
	return 1
}

for trial in $(seq 1 $1); do
	if [ -e /tmp/barely-t/data.img ]; then
		rm /tmp/barely-t/data.img
	fi
	: > /tmp/barely-t/logs/console.log 2>/dev/null
	serve_up
	docker pull -q alpine > /dev/null 2>&1
	docker volume create keep > /dev/null
	want=$(docker run --rm -v keep:/k alpine sh -c 'dd if=/dev/urandom of=/k/f bs=1M count=200 2>/dev/null; sha256sum /k/f | cut -c1-16')
	($B stop > /dev/null 2>&1 &)
	sleep $DELAY
	mid=$(docker run --rm -v keep:/k alpine sh -c 'sha256sum /k/f | cut -c1-16' 2>&1 | tail -1 | cut -c1-40)
	serve_gone
	guests=$(grep -c "Booting Linux" /tmp/barely-t/logs/console.log)
	serve_up
	got=$(docker run --rm -v keep:/k alpine sh -c 'sha256sum /k/f | cut -c1-16' 2>&1 | tail -1)
	errs=$(grep -c "EXT4-fs error" /tmp/barely-t/logs/console.log)
	echo "trial $trial: wrote $want, during stop $mid, after restart $got, ext4 errors $errs, boots before restart $guests"
	$B stop > /dev/null 2>&1
	serve_gone
done
