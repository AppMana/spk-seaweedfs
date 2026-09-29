#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
TEST_DIR=$(mktemp -d)
trap 'rm -rf "$TEST_DIR"' EXIT
mkdir -p "$TEST_DIR/var/run" "$TEST_DIR/var/log" "$TEST_DIR/target/bin"
cat > "$TEST_DIR/target/bin/synology-volume-bootstrap" <<'EOF'
#!/bin/sh
set -eu
count=0
[ ! -f "$TEST_DIR/bootstrap-count" ] || count=$(cat "$TEST_DIR/bootstrap-count")
count=$((count + 1))
printf '%s\n' "$count" > "$TEST_DIR/bootstrap-count"
[ "$count" -ne 1 ] || exit 75
printf '%s\n' '-dir=/unused' > "$SYNOPKG_PKGVAR/run/argv"
EOF
cat > "$TEST_DIR/target/bin/weed" <<'EOF'
#!/bin/sh
set -eu
[ "$(cat "$TEST_DIR/bootstrap-count")" = 2 ]
[ "$1" = volume ]
printf '%s\n' weed-started > "$TEST_DIR/result"
EOF
chmod +x "$TEST_DIR/target/bin/synology-volume-bootstrap" "$TEST_DIR/target/bin/weed"
export TEST_DIR
SYNOPKG_PKGVAR="$TEST_DIR/var" SYNOPKG_PKGDEST="$TEST_DIR/target" \
  timeout 15 sh "$ROOT/diyspk/seaweedfs/src/run.sh" 0
test "$(cat "$TEST_DIR/result")" = weed-started
test "$(cat "$TEST_DIR/bootstrap-count")" = 2
echo 'transient bootstrap failure recovery passed'
