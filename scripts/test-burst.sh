#!/bin/sh
# Burst-build scenario: a Node build peaking above 4 GB next to a
# steady service, in two shapes. Prints the VM's footprint on the Mac over
# time. Usage: scripts/test-burst.sh  (BARELY_HOME selects the install)
set -u
. "$(dirname "$0")/barely-target.sh"
BARELY=${BARELY:-./out/bin/barely}
B="$(cd "$(dirname "$0")/compat/burst" && pwd)"
fp() { $BARELY status | awk '/^memory:/{print $2 $3}'; }
guest() { $BARELY status | awk '/^guest:/{print "used " $3 $4 " cache " $6 $7}'; }
sample() { printf '%-34s vm=%-9s %s\n' "$1" "$(fp)" "$(guest)"; }

docker rm -f burst-steady burst-build burst-serve >/dev/null 2>&1
docker pull -q node:22-alpine >/dev/null
docker run -d --name burst-steady --platform linux/arm64 --tmpfs /m:size=2g alpine \
	sh -c 'dd if=/dev/zero of=/m/x bs=1M count=1500 2>/dev/null; sleep 100000' >/dev/null
sleep 8
sample "steady service only (1.5 GB)"

echo "== shape 1: build is its own process and exits"
for n in 1 2 3; do
	docker run --rm --name burst-build -v "$B":/s node:22-alpine \
		node --max-old-space-size=6000 /s/build.js once | sed "s/^/   run $n: /"
	sample "  run $n: right after exit"
	sleep 30
	sample "  run $n: +30s"
done
sleep 30
sample "  after 3 builds, +60s"

echo "== shape 2: one long-lived Node process (build, then keep serving)"
docker run -d --name burst-serve -v "$B":/s node:22-alpine \
	node --expose-gc --max-old-space-size=6000 /s/build.js serve >/dev/null
for t in 20 40 60 90 120; do
	prev=${prev:-0}
	sleep $((t - prev))
	prev=$t
	sample "  +${t}s"
done
docker logs burst-serve 2>&1 | grep -E 'BUILD|SERVE' | tail -3 | sed 's/^/   /'
docker rm -f burst-steady burst-serve >/dev/null 2>&1
sleep 30
sample "all stopped, +30s"
