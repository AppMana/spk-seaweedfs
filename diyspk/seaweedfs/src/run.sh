#!/bin/sh
# Actual daemon entrypoint, run by spksrc.service.start-stop-status.
#
# SERVICE_COMMAND must be a single bare word: start-stop-status reads it
# with `read -r` and then invokes it UNQUOTED (`${service} >> "${OUT}"
# 2>&1 &`), so any shell metacharacters in SERVICE_COMMAND itself
# (quotes, semicolons, redirects) get word-split and corrupted before
# they ever reach a shell that would interpret them. Putting the actual
# logic in this file sidesteps that: SERVICE_COMMAND is just this
# script's path (no spaces, no quoting needed), and this shell script
# provides its own real quoting once it's executing.
set -e

RUN_DIR="${SYNOPKG_PKGVAR}/run"
ARGV_FILE="${RUN_DIR}/argv"
WEED_BIN_FILE="${RUN_DIR}/weed_bin"
LOG_FILE="${SYNOPKG_PKGVAR}/log/weed.log"
BOOTSTRAP_BIN="${SYNOPKG_PKGDEST}/bin/synology-volume-bootstrap"
WEED_BIN="${SYNOPKG_PKGDEST}/bin/weed"

"${BOOTSTRAP_BIN}" \
  --config "${SYNOPKG_PKGVAR}/volume.yaml" \
  --out "${ARGV_FILE}" \
  --tls-dir "${SYNOPKG_PKGVAR}/tls" \
  --weed-bin-out "${WEED_BIN_FILE}" \
  >>"${LOG_FILE}" 2>&1

RUN_WEED="${WEED_BIN}"
if [ -s "${WEED_BIN_FILE}" ]; then
  W=$(head -n 1 "${WEED_BIN_FILE}")
  [ -n "$W" ] && RUN_WEED="$W"
fi

set --
while IFS= read -r line; do
  [ -n "$line" ] && set -- "$@" "$line"
done <"${ARGV_FILE}"

exec "${RUN_WEED}" volume "$@" >>"${LOG_FILE}" 2>&1
