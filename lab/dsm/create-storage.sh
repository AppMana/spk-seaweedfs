#!/usr/bin/env bash
# Create a storage pool + volume + the `seaweed` data directory on a
# freshly-installed DSM, over its webapi. DSM's Storage Manager has no
# scriptable CLI for this (synostgpool/synostgvolume are low-level and
# don't cover the full pool→volume flow) — the calls below are the
# same ones storage_panel.js's doCreateVolume()/doCreatePool() make;
# reverse-engineered from
# /var/packages/StorageManager/target/ui/storage_panel.js on the VM.
#
# Assumes a single data disk (sdb) with no existing pool. Idempotent:
# skips pool/volume creation if /volume1 already exists.
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
LAB="$(cd "$DIR/.." && pwd)"
MAC="${MAC:-52:54:00:12:de:ad}"
DSM_USER="${DSM_USER:?set DSM_USER}"
DSM_PASS="${DSM_PASS:?set DSM_PASS}"
DISK_ID="${DISK_ID:-sdb}"

DSM_IP=$(sudo awk -v mac="$MAC" 'tolower($2)==tolower(mac){print $3}' "$LAB/net/run/dnsmasq.leases" | tail -1)
[ -n "$DSM_IP" ] || { echo "no DHCP lease for $MAC" >&2; exit 1; }
BASE="http://$DSM_IP:5000/webapi"
SSH=(sshpass -p "$DSM_PASS" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "$DSM_USER@$DSM_IP")

if "${SSH[@]}" 'df /volume1' >/dev/null 2>&1; then
  echo "/volume1 already exists, skipping pool/volume creation"
else
  SID=$(curl -sf --max-time 10 "$BASE/entry.cgi" \
    --data-urlencode "api=SYNO.API.Auth" --data-urlencode "version=6" --data-urlencode "method=login" \
    --data-urlencode "account=$DSM_USER" --data-urlencode "passwd=$DSM_PASS" \
    --data-urlencode "session=StorageManager" --data-urlencode "format=sid" \
    | python3 -c 'import sys,json; print(json.load(sys.stdin)["data"]["sid"])')
  [ -n "$SID" ] || { echo "DSM login failed" >&2; exit 1; }

  # Pool: single disk, no RAID, no LVM ("-t single" — DSM calls this an
  # "unused"/reuse pool; the volume attaches directly, no vg1000).
  "${SSH[@]}" "echo '$DSM_PASS' | sudo -S /usr/syno/sbin/synostgpool --create -t single -l basic -c -d swlab /dev/$DISK_ID" >/dev/null
  echo "pool created on /dev/$DISK_ID, waiting for md assembly"
  for i in $(seq 1 30); do
    "${SSH[@]}" "grep -q md2 /proc/mdstat" 2>/dev/null && break
    sleep 5
  done

  SPACE_PATH=$(curl -sf --max-time 10 "$BASE/entry.cgi?api=SYNO.Storage.CGI.Storage&version=1&method=load_info&_sid=$SID" \
    | python3 -c 'import sys,json; d=json.load(sys.stdin)["data"]; print(d["storagePools"][0]["space_path"])')
  echo "pool space_path=$SPACE_PATH; creating btrfs volume"
  curl -sf --max-time 15 "$BASE/entry.cgi" \
    --data-urlencode "api=SYNO.Storage.CGI.Volume" --data-urlencode "method=deploy_unused" --data-urlencode "version=1" \
    --data-urlencode "_sid=$SID" --data-urlencode "space_path=$SPACE_PATH" --data-urlencode "fs_type=btrfs" \
    --data-urlencode "vol_attr=generic" --data-urlencode "vol_desc=swlab" --data-urlencode "atime_opt=relatime" \
    --data-urlencode "force=false" --data-urlencode "enable_dedupe=false" | grep -q '"success":true' \
    || { echo "volume create failed" >&2; exit 1; }

  echo "waiting for /volume1 to mount"
  for i in $(seq 1 30); do
    "${SSH[@]}" 'df /volume1' >/dev/null 2>&1 && break
    sleep 5
  done
fi

"${SSH[@]}" "echo '$DSM_PASS' | sudo -S mkdir -p /volume1/seaweed && echo '$DSM_PASS' | sudo -S chmod 777 /volume1/seaweed"
echo "ready: /volume1/seaweed"
