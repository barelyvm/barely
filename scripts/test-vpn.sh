#!/bin/sh
# Container networking checks, run once per VPN state (off, on, toggled).
# Usage: scripts/test-vpn.sh [label] [private-host]
# private-host: optional name or IP only reachable through the VPN.
set -u
. "$(dirname "$0")/barely-target.sh"
LABEL=${1:-run}
PRIV=${2:-}
pass() { echo "PASS [$LABEL] $*"; }
fail() { echo "FAIL [$LABEL] $*"; }
c() { docker run --rm --platform linux/arm64 alpine sh -c "$1" 2>&1; }

echo "INFO [$LABEL] mac default route via $(route -n get default 2>/dev/null | awk '/interface/{print $2}'), dns $(scutil --dns | awk '/nameserver\[0\]/{print $3; exit}')"
docker pull -q alpine >/dev/null 2>&1 && pass "docker pull" || fail "docker pull"
out=$(c 'wget -qO- -T 10 https://example.com | head -c 15')
case $out in *doctype*|*DOCTYPE*) pass "container HTTPS to the internet" ;; *) fail "container HTTPS: $out" ;; esac
out=$(c 'nslookup github.com 2>&1 | grep -c Address')
[ "${out:-0}" -ge 2 ] && pass "container DNS" || fail "container DNS: $out"
egress=$(c 'wget -qO- -T 10 https://ifconfig.me/ip')
echo "INFO [$LABEL] container egress IP $egress (Mac: $(curl -s -m 10 https://ifconfig.me/ip))"

docker rm -f barely-vpn-web >/dev/null 2>&1
docker run -d --name barely-vpn-web -p 18480:80 nginx:alpine >/dev/null 2>&1
sleep 2
curl -s -m 5 localhost:18480 | grep -q nginx && pass "published port on localhost" || fail "published port"
docker rm -f barely-vpn-web >/dev/null 2>&1

python3 -m http.server 18481 --bind 127.0.0.1 >/dev/null 2>&1 &
srv=$!
sleep 1
out=$(c 'wget -qO- -T 5 http://host.docker.internal:18481/ | head -c 40')
kill $srv 2>/dev/null
case $out in *Directory*|*DOCTYPE*|*html*) pass "host.docker.internal reaches the Mac" ;; *) fail "host.docker.internal: $out" ;; esac

if [ -n "$PRIV" ]; then
	out=$(c "ping -c1 -W3 $PRIV >/dev/null 2>&1 && echo up || (nc -z -w3 $PRIV 22 && echo up) || echo down")
	[ "$out" = up ] && pass "private host $PRIV reachable" || fail "private host $PRIV: $out"
fi
