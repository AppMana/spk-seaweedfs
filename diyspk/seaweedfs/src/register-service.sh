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
pkg=/var/packages/seaweedfs
unit=pkg-seaweedfs-volume.service
test -f "$pkg/INFO"
cmp "$pkg/conf/systemd/$unit" "/usr/local/lib/systemd/system/$unit"
grep -qx 'Before=pkgctl-seaweedfs.service' "$pkg/conf/systemd/$unit"
grep -qx 'Requires=pkg-volume.target' "$pkg/conf/systemd/$unit"
grep -qx 'After=pkg-volume.target' "$pkg/conf/systemd/$unit"
grep -qx 'RequiredBy=pkgctl-seaweedfs.service' "$pkg/conf/systemd/$unit"
grep -qx 'User=sc-seaweedfs' "$pkg/conf/systemd/$unit"
systemctl daemon-reload
systemctl enable "$unit"
systemctl daemon-reload
systemctl show pkgctl-seaweedfs.service -p Requires | tr ' =' '\n' | grep -qx "$unit"
echo 'DSM_SERVICE_REGISTERED: use synopkg start/stop seaweedfs for normal control'
