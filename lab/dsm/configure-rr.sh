#!/usr/bin/env bash
# Headless RR loader configuration — replaces the interactive TUI pass.
#
# RR's menu.sh dispatches directly to its internal functions when given
# arguments (and stubs out dialog), so the whole model/version/build
# flow is scriptable over SSH (root:rr on the booted RR image). The only
# env it needs is LOADER_DISK, which the interactive console gets from
# its boot shell.
#
# After `boot` the VM reboots into the DSM installer: continue at
# http://<vm-ip>:5000 (installer download runs unattended with
# `install.sh` — see README).
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
LAB="$(cd "$DIR/.." && pwd)"
MAC="${MAC:-52:54:00:12:de:ad}"
MODEL="${MODEL:-DS3622xs+}"
PLATFORM="${PLATFORM:-broadwellnk}"
PRODUCTVER="${PRODUCTVER:-7.2}"
RR_PASS="${RR_PASS:-rr}"

DSM_IP=$(sudo awk -v mac="$MAC" 'tolower($2)==tolower(mac){print $3}' "$LAB/net/run/dnsmasq.leases" | tail -1)
[ -n "$DSM_IP" ] || { echo "no DHCP lease for $MAC" >&2; exit 1; }
RR=(sshpass -p "$RR_PASS" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "root@$DSM_IP")

echo "RR at $DSM_IP; fetching pat list for $MODEL $PRODUCTVER"
PATURL=$("${RR[@]}" "python3 /opt/rr/include/functions.py getpats4mv -m $MODEL -v $PRODUCTVER" \
  | python3 -c 'import sys,json; d=json.load(sys.stdin); k=sorted(d,reverse=True)[0]; print(d[k]["url"])')
echo "pat: $PATURL"

"${RR[@]}" "export LOADER_DISK=/dev/sda
  /opt/rr/menu.sh reconfiguringM '$MODEL' '$PLATFORM' >/dev/null 2>&1
  /opt/rr/menu.sh reconfiguringV '$PRODUCTVER' '$PATURL' '00000000000000000000000000000000' >/dev/null 2>&1
  sed -i 's|^mac1:.*|mac1: \"$(echo "$MAC" | tr -d :)\"|' /mnt/p1/user-config.yml
  grep -E '^(model|platform|productver|paturl|sn|mac1):' /mnt/p1/user-config.yml"

echo "building loader (downloads the pat; several minutes)..."
"${RR[@]}" "export LOADER_DISK=/dev/sda; /opt/rr/menu.sh make 2>&1 | tail -3"

echo "rebooting into DSM installer"
"${RR[@]}" "export LOADER_DISK=/dev/sda; nohup /opt/rr/menu.sh boot >/dev/null 2>&1 &" || true
echo "DSM installer will come up at http://$DSM_IP:5000 in a few minutes"
