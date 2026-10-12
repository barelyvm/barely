#!/bin/sh
# A container does DNS + HTTPS every 2 s while the VPN is toggled.
# VPN_ON and VPN_OFF are the commands that connect and disconnect it.
set -u
. "$(dirname "$0")/barely-target.sh"
: "${VPN_ON:?set VPN_ON}" "${VPN_OFF:?set VPN_OFF}"
docker rm -f barely-vpn-loop >/dev/null 2>&1
docker run -d --name barely-vpn-loop --platform linux/arm64 alpine sh -c \
	'while true; do if wget -qO- -T 3 https://example.com >/dev/null 2>&1; then r=OK; else r=FAIL; fi; echo "$(date +%T) $r"; sleep 2; done' >/dev/null
step() { echo "== $(date +%T) $1"; eval "$2"; sleep "$3"; }
step "baseline (current state)" true 10
step "VPN off" "$VPN_OFF" 30
step "VPN on" "$VPN_ON" 30
step "VPN off" "$VPN_OFF" 30
step "VPN on" "$VPN_ON" 30
docker logs barely-vpn-loop 2>&1
docker rm -f barely-vpn-loop >/dev/null
