#!/usr/bin/env bash
# Fetch the RR (rr-loader) xpenology boot image into out/rr.img.
#
# RR release availability has churned historically; override RR_URL to
# point at a mirror (e.g. wjz304's continuation builds) if the RROrg
# release disappears. Any RR >= 24.x works for DSM 7.2 on DS3622xs+.
set -euo pipefail

OUT_DIR="$(cd "$(dirname "$0")" && pwd)/out"
RR_VERSION="${RR_VERSION:-26.7.3}"
RR_URL="${RR_URL:-https://github.com/RROrg/rr/releases/download/${RR_VERSION}/rr-${RR_VERSION}.img.zip}"
mkdir -p "$OUT_DIR"

if [ -f "$OUT_DIR/rr.img" ]; then
  echo "out/rr.img already present"
  exit 0
fi
echo "fetching $RR_URL"
curl -L -o "$OUT_DIR/rr.img.zip" "$RR_URL"
unzip -o "$OUT_DIR/rr.img.zip" -d "$OUT_DIR"
# The zip contains rr.img at its top level.
[ -f "$OUT_DIR/rr.img" ] || { echo "rr.img missing after unzip" >&2; exit 1; }
echo "ready: $OUT_DIR/rr.img"
