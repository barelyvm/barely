#!/bin/sh
# Live compatibility run: compose, buildx, testcontainers-go,
# Air, Node fs.watch and Tilt against the barely at DOCKER_HOST.
set -u
. "$(dirname "$0")/barely-target.sh"
cd "$(dirname "$0")/compat"
pass() { echo "PASS $*"; }
fail() { echo "FAIL $*"; }
t() { python3 -c 'import time;print(time.time())'; }

# compose
(cd compose && docker compose up -d >/dev/null 2>&1)
sleep 3
if curl -s localhost:18380 | grep -q nginx && docker compose -f compose/compose.yaml logs probe 2>/dev/null | grep -q probe-ok; then
	pass "compose: nginx on localhost:18380, service-to-service DNS"
else
	fail "compose"
fi
(cd compose && docker compose down -v >/dev/null 2>&1)

# buildx (BuildKit, multi-stage, cache mount)
if docker buildx build -q --load -t barely-buildx-test buildx >/dev/null 2>&1 && docker run --rm barely-buildx-test | grep -q buildx-ok; then
	pass "buildx: multi-stage build with cache mount"
else
	fail "buildx"
fi

# testcontainers-go (resource reaper mounts the docker socket)
if (cd tc && go test -count=1 ./... >/tmp/barely-tc.log 2>&1); then
	pass "testcontainers-go: redis up, reaped"
else
	fail "testcontainers-go: $(tail -3 /tmp/barely-tc.log | tr '\n' ' ')"
fi

# Air: rebuild on a Mac-side edit, no polling
AIR=$(mktemp -d "$HOME/.barely-air.XXXX")
cp air/main.go air/go.mod air/.air.toml "$AIR/"
docker rm -f barely-air >/dev/null 2>&1
docker run -d --name barely-air -v "$AIR":/app -w /app golang:1.23-alpine \
	sh -c 'go install github.com/air-verse/air@v1.61.7 && air' >/dev/null
for _ in $(seq 1 300); do docker logs barely-air 2>&1 | grep -q 'air-app starting v0' && break; sleep 1; done
before=$(docker logs barely-air 2>&1 | grep -c 'air-app starting')
t0=$(t)
sed -i '' 's/const version = "v0"/const version = "v1"/' "$AIR/main.go"
ok=
for _ in $(seq 1 100); do
	if docker logs barely-air 2>&1 | grep -q 'air-app starting v1'; then ok=1; break; fi
	sleep 0.1
done
if [ -n "$ok" ]; then
	pass "Air: rebuilt after Mac edit in $(python3 -c "import time;print('%.1fs'%(time.time()-$t0))") (includes go build)"
else
	fail "Air: no rebuild after edit"
fi
for i in $(seq 1 300); do echo "package main" > "$AIR/gen$i.go"; done
sleep 6
after=$(docker logs barely-air 2>&1 | grep -c 'air-app starting')
echo "INFO Air: 300-file burst caused $((after - before - 1)) rebuild(s)"
docker rm -f barely-air >/dev/null; rm -rf "$AIR"

# Node fs.watch (what Vite/chokidar use)
NW=$(mktemp -d "$HOME/.barely-nw.XXXX")
docker rm -f barely-nw >/dev/null 2>&1
docker run -d --name barely-nw -v "$NW":/w -v "$PWD/nodewatch":/s node:22-alpine node /s/watch.js >/dev/null
sleep 3
echo hi > "$NW/index.js"; sleep 1
if docker logs barely-nw 2>&1 | grep -q 'EVENT.*index.js'; then pass "Node fs.watch sees Mac edits"; else fail "Node fs.watch"; fi
docker rm -f barely-nw >/dev/null; rm -rf "$NW"

# Tilt: docker_build (BuildKit session) + compose + live_update
if (cd tilt && tilt ci --timeout 5m >/tmp/barely-tilt.log 2>&1); then
	pass "Tilt: tilt ci built and started the compose resource"
else
	fail "Tilt: $(tail -3 /tmp/barely-tilt.log | tr '\n' ' ')"
fi
(cd tilt && tilt down >/dev/null 2>&1)
