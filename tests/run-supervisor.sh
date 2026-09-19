#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# The fake weed records what it was handed and, on a mismatch, exits 0 rather
# than nonzero. A nonzero exit is a crash to the supervisor, which restarts it
# with doubling backoff -- so an assertion failure used to hang CI for as long
# as the harness allowed instead of failing. Exit clean, leave the reason in a
# file, and let the assertions below report it.
cat >"$TMP/fake-weed" <<'EOF'
#!/bin/sh
fail() { printf '%s\n' "$1" >"$MISMATCH"; exit 0; }
[ "$GOMEMLIMIT" = "$EXPECT_GOMEMLIMIT" ] || fail "GOMEMLIMIT=$GOMEMLIMIT want $EXPECT_GOMEMLIMIT"
[ "$1" = volume ] || fail "argv1=$1"
[ "$2" = -concurrentUploadLimitMB=3072 ] || fail "argv2=$2"
[ "$3" = -concurrentDownloadLimitMB=1024 ] || fail "argv3=$3"
[ "$4" = -readBufferSizeMB=1 ] || fail "argv4=$4"
count=0
[ ! -f "$SUPERVISOR_COUNT" ] || count=$(cat "$SUPERVISOR_COUNT")
count=$((count + 1))
printf '%s\n' "$count" >"$SUPERVISOR_COUNT"
[ "$count" -gt 1 ] && exit 0
exit 137
EOF
chmod +x "$TMP/fake-weed"

# run_case <name> <expected GOMEMLIMIT> [volume.yaml instances value]
run_case() {
  CASE=$1
  EXPECT=$2
  INSTANCES=${3:-}

  CASEDIR="$TMP/$CASE"
  PKGVAR="$CASEDIR/var"
  PKGDEST="$CASEDIR/target"
  mkdir -p "$PKGVAR/run" "$PKGVAR/log" "$PKGDEST/bin"
  printf '%s\n' '-dir=/unused' >"$PKGVAR/run/argv.1"
  printf '%s\n' "$TMP/fake-weed" >"$PKGVAR/run/weed_bin"
  [ -z "$INSTANCES" ] || printf 'volume:\n  instances: %s\n' "$INSTANCES" >"$PKGVAR/volume.yaml"

  set +e
  SYNOPKG_PKGVAR="$PKGVAR" \
  SYNOPKG_PKGDEST="$PKGDEST" \
  SUPERVISOR_COUNT="$CASEDIR/count" \
  MISMATCH="$CASEDIR/mismatch" \
  EXPECT_GOMEMLIMIT="$EXPECT" \
    sh "$ROOT/diyspk/seaweedfs/src/run.sh" 1
  status=$?
  set -e

  [ ! -f "$CASEDIR/mismatch" ] || {
    echo "$CASE: $(cat "$CASEDIR/mismatch")" >&2
    exit 1
  }
  [ "$status" -eq 0 ] || {
    echo "$CASE: run.sh exited $status instead of supervising the failed child" >&2
    exit 1
  }
  [ "$(cat "$CASEDIR/count")" -eq 2 ] || {
    echo "$CASE: run.sh did not restart weed after exit 137" >&2
    exit 1
  }
  grep -q 'weed died with status 137; restarting in 1s' "$PKGVAR/log/weed.1.log"
  grep -q 'weed exited cleanly; not restarting' "$PKGVAR/log/weed.1.log"
}

# No volume.yaml: fall back to a single instance and the whole budget.
run_case single 3072MiB

# volume.instances: 2 is what appmana-017-ds runs, because the "011" policy
# needs sameRackCount + 1 data nodes in a rack. The budget is divided rather
# than applied per instance, so two servers cannot target 6 GiB inside the 5 G
# MemoryMax the package installs.
run_case two_instances 1536MiB 2

echo "run.sh supervisor contract passed"
