#!/usr/bin/env bash
# Bring up the lab network fabric.
#
# Joins the EXISTING docker network named `kind` (shared with other kind
# clusters on this host — never created, modified, or deleted here) and
# adds:
#   - tap-dsm        tap device for the xpenology QEMU VM
#   - veth-cn/veth-kind  patch between the containernet switch and the
#                    kind bridge (veth-kind side enslaved here; veth-cn
#                    is enslaved to the containernet LinuxBridge by
#                    topology.py, or directly to the kind bridge in
#                    no-containernet mode)
#   - DOCKER-USER ACCEPTs so bridged/routed lab traffic is not dropped
#   - a scoped dnsmasq DHCP for the DSM installer (high address range,
#     clear of docker's sequential low allocations)
#
# Everything is additive and reversed by fabric-down.sh.
set -euo pipefail

LAB_USER="${SUDO_USER:-$(id -un)}"
NET_NAME=kind
POD_CIDR="${POD_CIDR:-10.244.0.0/16}"
DHCP_LOW_OCTETS="${DHCP_LOW_OCTETS:-200.10}"
DHCP_HIGH_OCTETS="${DHCP_HIGH_OCTETS:-200.50}"
RUN_DIR="$(cd "$(dirname "$0")" && pwd)/run"

[ "$(id -u)" -eq 0 ] || { echo "run with sudo" >&2; exit 1; }
mkdir -p "$RUN_DIR"

# Discover the existing kind network's bridge and IPv4 subnet.
NET_ID=$(docker network inspect "$NET_NAME" -f '{{.Id}}')
BRIDGE="br-${NET_ID:0:12}"
SUBNET=$(docker network inspect "$NET_NAME" -f '{{range .IPAM.Config}}{{.Subnet}}{{"\n"}}{{end}}' | grep -v : | head -1)
GATEWAY=$(docker network inspect "$NET_NAME" -f '{{range .IPAM.Config}}{{.Gateway}}{{"\n"}}{{end}}' | grep -v : | head -1)
ip link show "$BRIDGE" >/dev/null
PREFIX2=$(echo "$SUBNET" | cut -d. -f1-2)   # e.g. 172.21
{
  echo "BRIDGE=$BRIDGE"
  echo "SUBNET=$SUBNET"
  echo "GATEWAY=$GATEWAY"
  echo "POD_CIDR=$POD_CIDR"
} > "$RUN_DIR/fabric.env"
echo "kind network: bridge=$BRIDGE subnet=$SUBNET gw=$GATEWAY"

# Debug-host image for topology.py.
docker image inspect swlab-debug:latest >/dev/null 2>&1 || \
  docker build -q -t swlab-debug:latest -f "$(dirname "$0")/Dockerfile.debug" "$(dirname "$0")" >/dev/null

# Tap for the DSM VM.
if ! ip link show tap-dsm >/dev/null 2>&1; then
  ip tuntap add dev tap-dsm mode tap user "$LAB_USER"
fi
ip link set tap-dsm up

# veth patch containernet-switch <-> kind bridge.
if ! ip link show veth-kind >/dev/null 2>&1; then
  ip link add veth-cn type veth peer name veth-kind
fi
ip link set veth-kind master "$BRIDGE" up
ip link set veth-cn up

# Forwarding allowances. Same-bridge traffic can traverse iptables when
# br_netfilter is loaded; routed pod traffic always does. Insert exact
# rules (idempotent via check-first) so fabric-down can remove them.
add_rule() {
  iptables -C DOCKER-USER "$@" 2>/dev/null || iptables -I DOCKER-USER "$@"
}
add_rule -s "$SUBNET" -d "$SUBNET" -j ACCEPT
add_rule -s "$SUBNET" -d "$POD_CIDR" -j ACCEPT
add_rule -s "$POD_CIDR" -d "$SUBNET" -j ACCEPT

# Scoped DHCP for the DSM VM. Range sits high in the /16, far from
# docker's sequential low allocations; static leases are not needed —
# provision.sh discovers the VM by MAC from the lease file.
DNSMASQ_PID="$RUN_DIR/dnsmasq.pid"
if [ -f "$DNSMASQ_PID" ] && kill -0 "$(cat "$DNSMASQ_PID")" 2>/dev/null; then
  echo "dnsmasq already running (pid $(cat "$DNSMASQ_PID"))"
else
  dnsmasq \
    --interface="$BRIDGE" --bind-interfaces --except-interface=lo \
    --dhcp-range="${PREFIX2}.${DHCP_LOW_OCTETS},${PREFIX2}.${DHCP_HIGH_OCTETS},255.255.0.0,12h" \
    --dhcp-option=3,"$GATEWAY" \
    --dhcp-option=6,1.1.1.1 \
    --port=0 \
    --pid-file="$DNSMASQ_PID" \
    --dhcp-leasefile="$RUN_DIR/dnsmasq.leases"
  echo "dnsmasq up on $BRIDGE (${PREFIX2}.${DHCP_LOW_OCTETS}-${DHCP_HIGH_OCTETS})"
fi

echo "fabric up. next: topology.py (containernet) or attach tap-dsm directly:"
echo "  sudo ip link set tap-dsm master $BRIDGE   # no-containernet fallback"
