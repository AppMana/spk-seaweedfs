#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
CROSS_MAKEFILE="$ROOT/cross/seaweedfs/Makefile"
DIGESTS="$ROOT/cross/seaweedfs/digests"

expected_version=4.51-appmana.lab.2
expected_commit=1e38202609dc8fb39a77f1f72519ac7393efa79d
expected_go_fuse_commit=ee4fb2f73956e336d851e77993fcb3702e38e62c

value() {
  sed -n "s/^$1[[:space:]]*=[[:space:]]*//p" "$CROSS_MAKEFILE"
}

[ "$(value PKG_VERS)" = "$expected_version" ] || {
  echo "bundled weed must be $expected_version" >&2
  exit 1
}
[ "$(value PKG_COMMIT)" = "$expected_commit" ] || {
  echo "bundled weed must come from AppMana commit $expected_commit" >&2
  exit 1
}
[ "$(value PKG_DIST_SITE)" = "https://github.com/AppMana/forks-seaweedfs/archive" ] || {
  echo "bundled weed must come from the AppMana fork" >&2
  exit 1
}
[ "$(value GO_FUSE_COMMIT)" = "$expected_go_fuse_commit" ] || {
  echo "bundled weed must pin its AppMana go-fuse sibling" >&2
  exit 1
}
[ -n "$(value GO_FUSE_SHA256)" ] || {
  echo "bundled go-fuse archive must have a SHA-256 digest" >&2
  exit 1
}
grep -q "^seaweedfs-$expected_version.tar.gz SHA256 " "$DIGESTS" || {
  echo "missing digest for bundled weed $expected_version" >&2
  exit 1
}

echo "package source contract passed"
