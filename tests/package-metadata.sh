#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
PACKAGE_MAKEFILE="$ROOT/diyspk/seaweedfs/Makefile"
description=$(sed -n 's/^DESCRIPTION[[:space:]]*=[[:space:]]*//p' "$PACKAGE_MAKEFILE")

case "$description" in
  *'`'*|*'$('* )
    echo "DESCRIPTION contains shell command substitution syntax" >&2
    exit 1
    ;;
esac

echo "package metadata contract passed"
