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
[ "${GOMEMLIMIT-unset}" = "$EXPECT_GOMEMLIMIT" ] || fail "GOMEMLIMIT=${GOMEMLIMIT-unset} want $EXPECT_GOMEMLIMIT"
[ "$1" = volume ] || fail "argv1=$1"
[ "$2" = -readBufferSizeMB=1 ] || fail "argv2=$2"
[ "$3" = -dir=/unused ] || fail "argv3=$3"
[ "$#" -eq 3 ] || fail "unexpected extra arguments: $#"
count=0
[ ! -f "$SUPERVISOR_COUNT" ] || count=$(cat "$SUPERVISOR_COUNT")
count=$((count + 1))
printf '%s\n' "$count" >"$SUPERVISOR_COUNT"
[ "$count" -gt 1 ] && exit 0
exit 137
EOF
chmod +x "$TMP/fake-weed"

# run_case <name> <expected GOMEMLIMIT> [the bootstrap's gomemlimit.1 for this server]
run_case() {
  CASE=$1
  EXPECT=$2
  BUDGET=${3:-}

  CASEDIR="$TMP/$CASE"
  PKGVAR="$CASEDIR/var"
  PKGDEST="$CASEDIR/target"
  mkdir -p "$PKGVAR/run" "$PKGVAR/log" "$PKGDEST/bin"
  printf '%s\n' '-dir=/unused' >"$PKGVAR/run/argv.1"
  printf '%s\n' "$TMP/fake-weed" >"$PKGVAR/run/weed_bin"
  [ -z "$BUDGET" ] || printf '%s\n' "$BUDGET" >"$PKGVAR/run/gomemlimit.1"

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

# A lone server has no budget file and lets the core derive memory from
# DSM's cgroup; an empty override is removed rather than passed through.
unset GOMEMLIMIT
run_case single unset
GOMEMLIMIT='' run_case empty_override unset

# With several servers the bootstrap writes each one's budget (volume.servers
# memoryMB, or volume.instances' divided share); the package cgroup limit is
# their sum, so no two processes target the same memory.
run_case server_budget 3072MiB 3072MiB
GOMEMLIMIT='' run_case server_budget_empty_override 1536MiB 1536MiB
# An operator's explicit GOMEMLIMIT stays a per-process override.
GOMEMLIMIT=2048MiB run_case explicit_budget 2048MiB 1536MiB
GOMEMLIMIT=2048MiB run_case explicit_single_budget 2048MiB

echo "run.sh supervisor contract passed"
