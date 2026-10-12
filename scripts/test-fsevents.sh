#!/bin/sh
# Live acceptance for file-change events. Needs DOCKER_HOST
# pointing at a running barely and a writable project dir under a shared root.
set -u
. "$(dirname "$0")/barely-target.sh"
ROOT=${1:-$HOME/.barely-fsevent-test}
rm -rf "$ROOT" && mkdir -p "$ROOT/src"
LOG=$(mktemp)

watch() {
	docker rm -f barelyfs >/dev/null 2>&1
	docker run -d --name barelyfs --platform linux/arm64 -v "$ROOT":/w alpine \
		inotifyd - /w/src:cwmndy >/dev/null
	sleep 2
}
events() { docker logs barelyfs 2>&1; }
expect() { # name, pattern, action
	before=$(events | wc -l)
	eval "$3"
	t0=$(python3 -c 'import time;print(time.time())')
	for _ in $(seq 1 40); do
		if events | tail -n +$((before + 1)) | grep -q "$2"; then
			printf 'PASS %-34s %.2fs\n' "$1" "$(python3 -c "import time;print(time.time()-$t0)")"
			return
		fi
		sleep 0.05
	done
	echo "FAIL $1 (no '$2' in): $(events | tail -n +$((before + 1)) | tr '\n' '|')"
}

watch
expect "create on Mac"   "n.*a.go"  "echo 'package main' > '$ROOT/src/a.go'"
expect "edit on Mac"     "c.*a.go"  "echo '// edit' >> '$ROOT/src/a.go'"
expect "rename on Mac"   "n.*b.go"  "mv '$ROOT/src/a.go' '$ROOT/src/b.go'"
expect "atomic save (editor-style)" "[nc].*b.go" "echo '// saved' > '$ROOT/src/.b.go.tmp' && mv '$ROOT/src/.b.go.tmp' '$ROOT/src/b.go'"
expect "sed -i on Mac"   "[nc].*b.go"  "sed -i '' 's/saved/resaved/' '$ROOT/src/b.go'"
expect "delete on Mac"   "d.*b.go"  "rm '$ROOT/src/b.go'"

before=$(events | wc -l)
for i in $(seq 1 400); do echo "$i" > "$ROOT/src/f$i.txt"; done
sleep 3
n=$(events | tail -n +$((before + 1)) | grep -c 'f[0-9]*.txt')
echo "INFO burst of 400 creates -> $n events seen"

before=$(events | wc -l)
docker exec barelyfs sh -c 'echo from-container > /w/src/own.txt'
sleep 2
n=$(events | tail -n +$((before + 1)) | grep -c 'own.txt')
echo "INFO container's own write -> $n events (native write alone gives the baseline; no extra means no echo)"

docker restart barelyfs >/dev/null; sleep 2
expect "edit after container restart" "c.*f1.txt" "echo again >> '$ROOT/src/f1.txt'"

docker rm -f barelyfs >/dev/null
rm -rf "$ROOT" "$LOG"
