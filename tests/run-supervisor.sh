#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

PKGVAR="$TMP/var"
PKGDEST="$TMP/target"
mkdir -p "$PKGVAR/run" "$PKGVAR/log" "$PKGDEST/bin"
printf '%s\n' '-dir=/unused' >"$PKGVAR/run/argv.1"
printf '%s\n' "$TMP/fake-weed" >"$PKGVAR/run/weed_bin"

cat >"$TMP/fake-weed" <<'EOF'
#!/bin/sh
count=0
[ ! -f "$SUPERVISOR_COUNT" ] || count=$(cat "$SUPERVISOR_COUNT")
count=$((count + 1))
printf '%s\n' "$count" >"$SUPERVISOR_COUNT"
[ "$count" -gt 1 ] && exit 0
exit 137
EOF
chmod +x "$TMP/fake-weed"

set +e
SYNOPKG_PKGVAR="$PKGVAR" \
SYNOPKG_PKGDEST="$PKGDEST" \
SUPERVISOR_COUNT="$TMP/count" \
  sh "$ROOT/diyspk/seaweedfs/src/run.sh" 1
status=$?
set -e

[ "$status" -eq 0 ] || {
  echo "run.sh exited $status instead of supervising the failed child" >&2
  exit 1
}
[ "$(cat "$TMP/count")" -eq 2 ] || {
  echo "run.sh did not restart weed after exit 137" >&2
  exit 1
}
grep -q 'weed died with status 137; restarting in 1s' "$PKGVAR/log/weed.1.log"
grep -q 'weed exited cleanly; not restarting' "$PKGVAR/log/weed.1.log"

echo "run.sh supervisor contract passed"
