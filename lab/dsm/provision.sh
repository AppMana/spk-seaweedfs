#!/usr/bin/env bash
# Provision the DSM VM: install the SPK, write volume.yaml + kube
# credentials, add the pod-CIDR route, and (re)start the package.
#
# Prereqs (one-time, manual — see README.md): DSM installed, an admin
# user, SSH enabled, and a shared folder named "seaweed" on /volume1.
#
# Env:
#   DSM_USER / DSM_PASS   DSM admin credentials (required)
#   WEED_IMAGE            OCI ref for weed, empty = SPK-bundled binary
#   WEED_PLAIN_HTTP       true for the lab registry leg
#   WEED_DIGEST           optional digest pin
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
LAB="$(cd "$DIR/.." && pwd)"
REPO="$(cd "$LAB/.." && pwd)"
MAC="${MAC:-52:54:00:12:de:ad}"
: "${DSM_USER:?set DSM_USER}"
: "${DSM_PASS:?set DSM_PASS}"
WEED_IMAGE="${WEED_IMAGE:-}"
WEED_PLAIN_HTTP="${WEED_PLAIN_HTTP:-false}"
WEED_DIGEST="${WEED_DIGEST:-}"

# shellcheck disable=SC1091
. "$LAB/kind/run/cluster.env"    # APISERVER, CONTROL_PLANE_IP, SAN_OK

# VM IP from the lab dnsmasq lease (keyed on the stable MAC).
DSM_IP=$(sudo awk -v mac="$MAC" 'tolower($2)==tolower(mac){print $3}' "$LAB/net/run/dnsmasq.leases" | tail -1)
[ -n "$DSM_IP" ] || { echo "no DHCP lease for $MAC yet — is the VM up and DSM booted?" >&2; exit 1; }
echo "DSM at $DSM_IP"

SPK=$(find "$REPO/spksrc/packages" -name 'seaweedfs_*.spk' | head -1)
[ -n "$SPK" ] || { echo "no SPK built — run make in the repo root" >&2; exit 1; }

SSH=(sshpass -p "$DSM_PASS" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "$DSM_USER@$DSM_IP")
# DSM's sshd doesn't run an sftp-server by default (only forceEnableDSMTelnetSSH's
# plain SSH), so the sftp-based scp protocol fails with "subsystem
# request failed" — force the legacy scp exec protocol instead.
SCP=(sshpass -p "$DSM_PASS" scp -O -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null)
SUDO="echo '$DSM_PASS' | sudo -S -p ''"

"${SCP[@]}" "$SPK" "$LAB/kind/run/token" "$LAB/kind/run/ca.crt" "$DSM_USER@$DSM_IP:/tmp/"

"${SSH[@]}" "set -e
  $SUDO mkdir -p /volume1/seaweed && $SUDO chmod 777 /volume1/seaweed
  if ! $SUDO /usr/syno/bin/synopkg status seaweedfs >/dev/null 2>&1; then
    $SUDO /usr/syno/bin/synopkg install /tmp/$(basename "$SPK")
  fi
  $SUDO /usr/syno/bin/synopkg stop seaweedfs >/dev/null 2>&1 || true

  PKGVAR=/var/packages/seaweedfs/var
  $SUDO mkdir -p \$PKGVAR/kube \$PKGVAR/run \$PKGVAR/log \$PKGVAR/oci
  $SUDO chown sc-seaweedfs:synocommunity \$PKGVAR/oci
  $SUDO cp /tmp/token \$PKGVAR/kube/token
  $SUDO cp /tmp/ca.crt \$PKGVAR/kube/ca.crt
  $SUDO chmod 600 \$PKGVAR/kube/token
  DSM_LAN_IP=\$(ip -4 -o addr show scope global | awk '{print \$4}' | cut -d/ -f1 | head -1)
  $SUDO tee \$PKGVAR/volume.yaml >/dev/null <<EOF
kube:
  apiserver: ${APISERVER}
  tokenFile: /var/packages/seaweedfs/var/kube/token
  caFile: /var/packages/seaweedfs/var/kube/ca.crt
  insecureSkipTLSVerify: $([ "$SAN_OK" = true ] && echo false || echo true)
  namespace: seaweedfs
  seaweedName: seaweed1
volume:
  dir: /volume1/seaweed
  ip: \$DSM_LAN_IP
  publicUrl: \$DSM_LAN_IP:8080
  port: 8080
  grpcPort: 18080
  dataCenter: lab
  rack: synology
  max: 100
mtls:
  secretName: \"\"
weed:
  image: \"${WEED_IMAGE}\"
  digest: \"${WEED_DIGEST}\"
  plainHTTP: ${WEED_PLAIN_HTTP}
  binaries: []
  cacheDir: /var/packages/seaweedfs/var/oci
EOF
  $SUDO chown sc-seaweedfs:synocommunity \$PKGVAR/volume.yaml \$PKGVAR/kube/token \$PKGVAR/kube/ca.crt
  $SUDO chmod 600 \$PKGVAR/volume.yaml
  # Master discovery returns pod IPs; route them via the kind node.
  # Not persistent across VM reboots — this script re-asserts it.
  $SUDO ip route replace 10.244.0.0/16 via ${CONTROL_PLANE_IP}
  $SUDO /usr/syno/bin/synopkg start seaweedfs
  sleep 4
  $SUDO /usr/syno/bin/synopkg status seaweedfs
  $SUDO tail -5 \$PKGVAR/log/weed.log || true
"
echo "provisioned: weed.image='${WEED_IMAGE:-<bundled>}'"
