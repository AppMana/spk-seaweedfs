#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
CROSS_MAKEFILE="$ROOT/cross/seaweedfs/Makefile"
DIGESTS="$ROOT/cross/seaweedfs/digests"

expected_version=4.47-appmana.lab.5
expected_commit=dd2b9ef98d38488121808765148306c365e341b1
expected_go_fuse_commit=1bdeec4d57d1e9ee85d4938f36f2ed876dd7bd5e

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
