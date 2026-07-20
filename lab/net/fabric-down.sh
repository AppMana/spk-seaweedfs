#!/usr/bin/env bash
# Tear down everything fabric-up.sh created. Never touches the shared
# `kind` docker network or any containers on it.
set -uo pipefail

RUN_DIR="$(cd "$(dirname "$0")" && pwd)/run"
[ "$(id -u)" -eq 0 ] || { echo "run with sudo" >&2; exit 1; }

if [ -f "$RUN_DIR/fabric.env" ]; then
  # shellcheck disable=SC1091
  . "$RUN_DIR/fabric.env"
fi

if [ -f "$RUN_DIR/dnsmasq.pid" ]; then
  kill "$(cat "$RUN_DIR/dnsmasq.pid")" 2>/dev/null
  rm -f "$RUN_DIR/dnsmasq.pid"
fi

ip link del veth-cn 2>/dev/null       # removes veth-kind too
ip link del tap-dsm 2>/dev/null

if [ -n "${SUBNET:-}" ]; then
  iptables -D DOCKER-USER -s "$SUBNET" -d "$SUBNET" -j ACCEPT 2>/dev/null
  iptables -D DOCKER-USER -s "$SUBNET" -d "${POD_CIDR:-10.244.0.0/16}" -j ACCEPT 2>/dev/null
  iptables -D DOCKER-USER -s "${POD_CIDR:-10.244.0.0/16}" -d "$SUBNET" -j ACCEPT 2>/dev/null
fi

# Containernet leftovers — only after a crash of OUR topology. `mn -c`
# is global and this host has other containernet users, so never run it
# while a foreign topology may be live.
if [ -f "$RUN_DIR/topology.pid" ]; then
  if ! kill -0 "$(cat "$RUN_DIR/topology.pid")" 2>/dev/null; then
    command -v mn >/dev/null && mn -c >/dev/null 2>&1
  else
    echo "topology.py still running (pid $(cat "$RUN_DIR/topology.pid")); stop it first" >&2
  fi
  rm -f "$RUN_DIR/topology.pid"
fi

echo "fabric down"
