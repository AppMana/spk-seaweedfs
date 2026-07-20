#!/usr/bin/env bash
# Boot the xpenology DSM VM on the lab fabric.
#
# The VM's single NIC is tap-dsm, which fabric-up.sh created and
# topology.py enslaved to the containernet switch — so the DSM sits on
# the same L2 segment as the kind nodes, the lab registry, and the
# debug host, and gets its installer DHCP lease from the lab dnsmasq.
#
# First boot: open the RR configurator (http://<vm-ip>:7681 or the
# serial console), `model DS3622xs+`, `version 7.2`, `build`, `boot`;
# then install DSM from http://<vm-ip>:5000. See README.md.
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
OUT="$DIR/out"
MEM_MB="${MEM_MB:-4096}"
CPUS="${CPUS:-4}"
DATA_DISK_GB="${DATA_DISK_GB:-32}"
VNC_DISPLAY="${VNC_DISPLAY:-:9}"        # 127.0.0.1:5909
MAC="${MAC:-52:54:00:12:de:ad}"        # keep stable: DHCP lease + provision.sh discovery key on it
NIC_MODEL="${NIC_MODEL:-virtio-net-pci}"  # fallback: e1000e if RR misses virtio

[ -f "$OUT/rr.img" ] || { echo "run fetch-rr.sh first" >&2; exit 1; }
[ -f "$OUT/data.qcow2" ] || qemu-img create -f qcow2 "$OUT/data.qcow2" "${DATA_DISK_GB}G"

if [ -f "$OUT/qemu.pid" ] && kill -0 "$(cat "$OUT/qemu.pid")" 2>/dev/null; then
  echo "DSM VM already running (pid $(cat "$OUT/qemu.pid"))"
  exit 0
fi

# tap-dsm must exist and be enslaved (fabric-up.sh + topology.py).
ip link show tap-dsm >/dev/null

qemu-system-x86_64 \
  -name swlab-dsm \
  -enable-kvm -machine q35 -cpu host -smp "$CPUS" -m "$MEM_MB" \
  -drive file="$OUT/rr.img",format=raw,if=none,id=boot -device ide-hd,drive=boot,bus=ide.0 \
  -drive file="$OUT/data.qcow2",format=qcow2,if=none,id=data -device ide-hd,drive=data,bus=ide.1 \
  -netdev tap,id=n0,ifname=tap-dsm,script=no,downscript=no \
  -device "$NIC_MODEL",netdev=n0,mac="$MAC" \
  -vnc "127.0.0.1${VNC_DISPLAY}" \
  -serial file:"$OUT/serial.log" \
  -daemonize -pidfile "$OUT/qemu.pid"

echo "DSM VM up (VNC 127.0.0.1${VNC_DISPLAY#:*}, serial $OUT/serial.log)"
echo "watch the lab DHCP lease: sudo cat ../net/run/dnsmasq.leases | grep -i ${MAC}"
