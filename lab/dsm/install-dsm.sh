#!/usr/bin/env bash
# Kick off the DSM installation on the junior (installer) system via its
# CGI API — the same calls the web wizard makes. Internet-install mode:
# the installer downloads the pat RR configured (build_ver from RR's
# user-config). Wipes the VM's data disk (that's the point of the lab).
#
# Note: is_installing stays false unless the disk-wipe consent params
# are included; install.cgi returns success:true either way.
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
LAB="$(cd "$DIR/.." && pwd)"
MAC="${MAC:-52:54:00:12:de:ad}"
DSM_IP="${DSM_IP:-$(sudo awk -v mac="$MAC" 'tolower($2)==tolower(mac){print $3}' "$LAB/net/run/dnsmasq.leases" | tail -1)}"
[ -n "$DSM_IP" ] || { echo "no DHCP lease for $MAC" >&2; exit 1; }
BASE="http://$DSM_IP:5000/webman"

STATE=$(curl -sf --max-time 10 "$BASE/get_state.cgi")
echo "$STATE" | grep -q '"internet_install_ok": true' || {
  echo "installer not ready for internet install:"; echo "$STATE" | head -20; exit 1; }

curl -sf --max-time 15 "$BASE/install.cgi" -X POST \
  -d 'action=install_dsm&install_type=internet&clean_all_partition_disks=yes&agree_del_data=yes' >/dev/null
sleep 5
curl -sf --max-time 10 "$BASE/get_state.cgi" | grep -q '"is_installing": true' \
  && echo "DSM installing on $DSM_IP (10-15 min incl. reboot); watch http://$DSM_IP:5000" \
  || { echo "install did not start" >&2; exit 1; }
