#!/usr/bin/env bash
# End-to-end verification: the Synology (DSM VM) joined the operator
# cluster and actually stores data.
#
# Usage: e2e.sh <leg>    leg = fork | upstream | bundled
#   fork     expects weed.image = lab-registry fork build → `weed version` 8000GB
#   upstream expects weed.image = chrislusf/seaweedfs:latest → 30GB
#   bundled  expects no weed.image → SPK-bundled binary (8000GB, spksrc build)
#
# Env: DSM_USER / DSM_PASS as for provision.sh.
set -euo pipefail

LEG="${1:?leg: fork|upstream|bundled}"
DIR="$(cd "$(dirname "$0")" && pwd)"
LAB="$(cd "$DIR/.." && pwd)"
MAC="${MAC:-52:54:00:12:de:ad}"
: "${DSM_USER:?set DSM_USER}"
: "${DSM_PASS:?set DSM_PASS}"

# shellcheck disable=SC1091
. "$LAB/kind/run/cluster.env"
DSM_IP=$(sudo awk -v mac="$MAC" 'tolower($2)==tolower(mac){print $3}' "$LAB/net/run/dnsmasq.leases" | tail -1)
[ -n "$DSM_IP" ] || { echo "no DSM lease" >&2; exit 1; }
SSH=(sshpass -p "$DSM_PASS" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "$DSM_USER@$DSM_IP")
SUDO="echo '$DSM_PASS' | sudo -S -p ''"
KC=(kubectl --context kind-swlab -n seaweedfs)
pass=0; fail=0
ok()   { echo "PASS: $*"; pass=$((pass+1)); }
bad()  { echo "FAIL: $*"; fail=$((fail+1)); }

# 1. Binary provenance on the NAS.
WEED_BIN=$("${SSH[@]}" "$SUDO head -1 /var/packages/seaweedfs/var/run/weed_bin 2>/dev/null" || true)
VERSION=$("${SSH[@]}" "$SUDO \${WEED_BIN:-/var/packages/seaweedfs/target/bin/weed} version" 2>/dev/null | head -1 || true)
[ -n "$WEED_BIN" ] && VERSION=$("${SSH[@]}" "$SUDO $WEED_BIN version" 2>/dev/null | head -1 || true)
echo "weed_bin='$WEED_BIN'  version='$VERSION'"
case "$LEG" in
  fork)
    echo "$WEED_BIN" | grep -q "/var/packages/seaweedfs/var/oci/sha256-" \
      && ok "binary extracted from OCI cache" || bad "weed_bin not in OCI cache: '$WEED_BIN'"
    echo "$VERSION" | grep -q "8000GB" && ok "fork large_disk build (8000GB)" || bad "expected 8000GB, got: $VERSION"
    ;;
  upstream)
    echo "$WEED_BIN" | grep -q "/var/packages/seaweedfs/var/oci/sha256-" \
      && ok "binary extracted from OCI cache" || bad "weed_bin not in OCI cache: '$WEED_BIN'"
    echo "$VERSION" | grep -q "30GB" && ok "upstream build (30GB)" || bad "expected 30GB, got: $VERSION"
    ;;
  bundled)
    [ -z "$WEED_BIN" ] && ok "no OCI override (bundled binary)" || bad "unexpected weed_bin: $WEED_BIN"
    ;;
esac

# 2. Cluster membership: the synology rack appears in the master topology.
MASTER_POD=$("${KC[@]}" get pod -l app.kubernetes.io/component=master -o jsonpath='{.items[0].metadata.name}')
TOPO=$("${KC[@]}" exec "$MASTER_POD" -- wget -qO- http://127.0.0.1:9333/dir/status)
echo "$TOPO" | grep -q '"Id":"synology"' || echo "$TOPO" | grep -q 'synology' \
  && ok "rack 'synology' present in master topology" || bad "synology rack missing from /dir/status"
echo "$TOPO" | grep -q "$DSM_IP:8080" \
  && ok "volume server $DSM_IP:8080 registered" || bad "$DSM_IP:8080 not in topology"

# 3. Targeted write: assign on the synology rack, PUT, GET back, compare.
ASSIGN=$("${KC[@]}" exec "$MASTER_POD" -- wget -qO- "http://127.0.0.1:9333/dir/assign?dataCenter=lab&rack=synology")
FID=$(echo "$ASSIGN" | python3 -c 'import sys,json; print(json.load(sys.stdin)["fid"])')
URL=$(echo "$ASSIGN" | python3 -c 'import sys,json; print(json.load(sys.stdin)["url"])')
echo "assigned fid=$FID on $URL"
echo "$URL" | grep -q "$DSM_IP:8080" && ok "assignment landed on the synology" || bad "assigned to $URL, not the NAS"
PAYLOAD="spk-e2e-$LEG-$$"
D1="sudo docker exec mn.d1"
$D1 bash -c "echo -n '$PAYLOAD' > /tmp/e2e.txt && curl -sf -F file=@/tmp/e2e.txt http://$URL/$FID >/dev/null" \
  && ok "wrote to synology volume" || bad "write to $URL/$FID failed"
GOT=$($D1 curl -sf "http://$URL/$FID" || true)
[ "$GOT" = "$PAYLOAD" ] && ok "read back from synology matches" || bad "read mismatch: '$GOT'"

# 4. Filer smoke test through the cluster path.
FILER_POD=$("${KC[@]}" get pod -l app.kubernetes.io/component=filer -o jsonpath='{.items[0].metadata.name}')
FILER_IP=$("${KC[@]}" get pod "$FILER_POD" -o jsonpath='{.status.podIP}')
$D1 bash -c "curl -sf -F file=@/tmp/e2e.txt http://$FILER_IP:8888/lab/e2e-$LEG.txt >/dev/null && curl -sf http://$FILER_IP:8888/lab/e2e-$LEG.txt" | grep -q "$PAYLOAD" \
  && ok "filer write/read via fabric" || bad "filer path failed"

echo
echo "leg=$LEG: $pass passed, $fail failed"
exit $((fail > 0))
