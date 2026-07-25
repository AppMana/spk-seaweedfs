#!/bin/sh
# Actual daemon entrypoint, run by spksrc.service.start-stop-status —
# once per volume-server instance (SERVICE_COMMAND has one line per
# instance; see service-setup.sh).
#
# SERVICE_COMMAND lines must stay free of shell metacharacters:
# start-stop-status reads each line with `read -r` and invokes it
# UNQUOTED (`${service} >> "${OUT}" 2>&1 &`), so quoting/semicolons in
# the line itself get word-split and corrupted before any shell parses
# them. This script's path plus a bare numeric argument survives that;
# all real quoting lives in here.
#
# Instance 0 runs the bootstrap, which performs kube discovery once and
# renders argv files for every instance (argv, argv.1, ...) — weed_bin
# is written before any argv file. Instances > 0 just wait for their
# argv file to appear (service_prestart removed stale ones) and exec.
set -e

IDX="${1:-0}"
RUN_DIR="${SYNOPKG_PKGVAR}/run"
ARGV_FILE="${RUN_DIR}/argv"
[ "$IDX" != "0" ] && ARGV_FILE="${ARGV_FILE}.${IDX}"
WEED_BIN_FILE="${RUN_DIR}/weed_bin"
LOG_FILE="${SYNOPKG_PKGVAR}/log/weed.log"
[ "$IDX" != "0" ] && LOG_FILE="${SYNOPKG_PKGVAR}/log/weed.${IDX}.log"
BOOTSTRAP_BIN="${SYNOPKG_PKGDEST}/bin/synology-volume-bootstrap"
WEED_BIN="${SYNOPKG_PKGDEST}/bin/weed"

if [ "$IDX" = "0" ]; then
  "${BOOTSTRAP_BIN}" \
    --config "${SYNOPKG_PKGVAR}/volume.yaml" \
    --out "${RUN_DIR}/argv" \
    --tls-dir "${SYNOPKG_PKGVAR}/tls" \
    --weed-bin-out "${WEED_BIN_FILE}" \
    >>"${LOG_FILE}" 2>&1
else
  n=0
  while [ ! -s "${ARGV_FILE}" ] && [ "$n" -lt 120 ]; do
    sleep 1
    n=$((n + 1))
  done
  if [ ! -s "${ARGV_FILE}" ]; then
    echo "argv for instance ${IDX} never appeared (bootstrap failed?)" >>"${LOG_FILE}"
    exit 1
  fi
fi

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
