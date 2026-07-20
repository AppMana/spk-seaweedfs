#!/usr/bin/env bash
# Inject a DSM admin user + force-enable SSH via RR's once.d mechanism.
#
# RR's own menu.sh addNewDSMUser/forceEnableDSMTelnetSSH functions read
# from an interactive dialog form, so they're not scriptable directly —
# but both boil down to writing a small script into
# <DSM root>/usr/rr/once.d/, which RR's init runs once on DSM's first
# boot after the loader hands off. This does that step manually: mount
# the DSM system partition (still assembled from the earlier `make`
# step) under the RR environment and drop the script in ourselves.
#
# Must run while RR (not DSM) is booted and reachable over SSH
# (root:rr default). Run BEFORE configure-rr.sh's `boot` step, or
# before rebooting back into the RR loader if DSM is already up.
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
LAB="$(cd "$DIR/.." && pwd)"
MAC="${MAC:-52:54:00:12:de:ad}"
RR_PASS="${RR_PASS:-rr}"
DSM_USER="${DSM_USER:?set DSM_USER}"
DSM_PASS="${DSM_PASS:?set DSM_PASS}"

DSM_IP=$(sudo awk -v mac="$MAC" 'tolower($2)==tolower(mac){print $3}' "$LAB/net/run/dnsmasq.leases" | tail -1)
[ -n "$DSM_IP" ] || { echo "no DHCP lease for $MAC" >&2; exit 1; }
RR=(sshpass -p "$RR_PASS" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "root@$DSM_IP")

"${RR[@]}" "set -e
  ROOT=\$(ls -d /dev/md/SynologyNAS:0 2>/dev/null || echo /dev/md0)
  mkdir -p /tmp/mdX
  mount \"\$ROOT\" /tmp/mdX
  mkdir -p /tmp/mdX/usr/rr/once.d
  cat > /tmp/mdX/usr/rr/once.d/00-swlab-admin.sh <<EOS
#!/usr/bin/env bash
if synouser --enum local | grep -q '^${DSM_USER}\$'; then
  synouser --setpw ${DSM_USER} '${DSM_PASS}'
else
  synouser --add ${DSM_USER} '${DSM_PASS}' rr 0 user@rr.com 1
fi
synogroup --memberadd administrators ${DSM_USER}
systemctl restart inetd
synowebapi -s --exec api=SYNO.Core.Terminal method=set version=3 enable_telnet=true enable_ssh=true ssh_port=22 forbid_console=false
EOS
  chmod +x /tmp/mdX/usr/rr/once.d/00-swlab-admin.sh
  sync
  umount /tmp/mdX
  echo INJECTED"

echo "admin '$DSM_USER' + SSH will be provisioned on next DSM boot"
