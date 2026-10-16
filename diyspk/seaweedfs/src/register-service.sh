#!/bin/sh
# Administrator-side installation step. DSM installs native unit files but
# does not enable them, and package-identity hooks cannot control PID 1.
# No daemon is started here; subsequent synopkg controls own its lifecycle.
set -eu
PATH=/usr/syno/bin:/usr/syno/sbin:/usr/sbin:/usr/bin:/sbin:/bin
export PATH
if [ "$(id -u)" != 0 ]; then
  echo 'Run this registration step as an administrator with sudo.' >&2
  exit 1
fi
grep -q '^majorversion="7"$' /etc.defaults/VERSION
# The overrides exist for tests; registration runs with the defaults.
pkg=${SEAWEEDFS_PKG_DIR:-/var/packages/seaweedfs}
units=${SEAWEEDFS_UNIT_DIR:-/usr/local/lib/systemd/system}
fsroot=${SEAWEEDFS_FS_ROOT:-}
unit=pkg-seaweedfs-volume.service
test -f "$pkg/INFO"
cmp "$pkg/conf/systemd/$unit" "$units/$unit"
grep -qx 'Before=pkgctl-seaweedfs.service' "$pkg/conf/systemd/$unit"
grep -qx 'Requires=pkg-volume.target' "$pkg/conf/systemd/$unit"
grep -qx 'After=pkg-volume.target' "$pkg/conf/systemd/$unit"
grep -qx 'RequiredBy=pkgctl-seaweedfs.service' "$pkg/conf/systemd/$unit"
grep -qx 'User=sc-seaweedfs' "$pkg/conf/systemd/$unit"
bootstrap="$pkg/target/bin/synology-volume-bootstrap"
config="$pkg/var/volume.yaml"
# Each volume server owns a directory on its own DSM volume, which the
# unprivileged package cannot create at a volume's root. Create missing ones
# for the package user; only a plain /volumeN/<path> without symlinks is
# accepted, so the package's configuration cannot direct root elsewhere.
if [ -f "$config" ]; then
  # Captured, not piped, so a configuration the bootstrap rejects stops
  # registration instead of looking like an empty server list.
  servers=$("$bootstrap" --config "$config" --print-servers)
  printf '%s\n' "$servers" | while IFS="$(printf '\t')" read -r dir _; do
    [ -n "$dir" ] || continue
    case "$dir" in
      /volume[0-9]*/?*) ;;
      *) echo "volume server directory $dir is not under a DSM volume" >&2; exit 1 ;;
    esac
    case "$dir" in *..*) echo "volume server directory $dir is not canonical" >&2; exit 1 ;; esac
    volume=$(printf '%s\n' "$dir" | cut -d/ -f1-2)
    if ! grep -q " $volume " /proc/mounts; then
      echo "volume server directory $dir: $volume is not a mounted DSM volume" >&2
      exit 1
    fi
    if [ ! -e "$fsroot$dir" ]; then
      parent=$fsroot$(dirname "$dir")
      if [ ! -d "$parent" ] || [ -L "$parent" ]; then
        echo "volume server directory $dir: parent $parent is not a directory" >&2
        exit 1
      fi
      install -d -m 755 -o sc-seaweedfs -g synocommunity "$fsroot$dir"
    fi
    if [ ! -d "$fsroot$dir" ] || [ -L "$fsroot$dir" ]; then
      echo "volume server directory $dir is not a directory" >&2
      exit 1
    fi
  done
  # Several servers share this unit's cgroup: its limit is the sum of their
  # Go budgets plus page cache, not the single-server default in the unit.
  limit=$("$bootstrap" --config "$config" --print-memory-limit-mb)
else
  limit=0
fi
dropin="$units/$unit.d"
if [ "$limit" -gt 0 ]; then
  install -d -m 755 "$dropin"
  printf '[Service]\nMemoryLimit=%sM\n' "$limit" > "$dropin/memory.conf.tmp"
  mv "$dropin/memory.conf.tmp" "$dropin/memory.conf"
else
  rm -f "$dropin/memory.conf"
fi
systemctl daemon-reload
systemctl enable "$unit"
systemctl daemon-reload
systemctl show pkgctl-seaweedfs.service -p Requires | tr ' =' '\n' | grep -qx "$unit"
# DSM's installer can return while package payloads on /volume1 and native
# units/links on the system filesystem are still dirty. A power cut then left
# INFO at the new version, the appstore target missing and the unit zero bytes.
# Registration is the final administrator-side install/upgrade boundary, not
# a request-path operation. Flush both filesystems before reporting success.
# Do not put this barrier in a workload test or after its data writes.
sync
echo 'DSM_SERVICE_REGISTERED: use synopkg start/stop seaweedfs for normal control'
