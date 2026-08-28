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

# DSM starts services with the default 1024-descriptor limit, which a volume
# server cannot live within: it holds .dat + .idx + .ldb open per volume, so a
# few hundred volumes exhausts it before counting sockets. On 2026-08-05 this
# host was serving ~270 volumes when a replication pass added concurrent
# VolumeCopy streams; it hit "socket: too many open files", os.OpenFile then
# returned a nil *os.File, and NewDiskFile dereferenced it -- SIGSEGV, whole
# process dead, 359 volumes left under-replicated until someone looked.
#
# Raise to a hard-limit-capped 65536. Logged so the achieved value is visible
# in weed.log rather than inferred: the failure mode is silent until it is not.
FD_WANT=65536
FD_HARD=$(ulimit -Hn 2>/dev/null || echo "$FD_WANT")
[ "$FD_HARD" = "unlimited" ] && FD_HARD=$FD_WANT
[ "$FD_WANT" -gt "$FD_HARD" ] 2>/dev/null && FD_WANT=$FD_HARD
ulimit -n "$FD_WANT" 2>/dev/null || echo "WARN: could not raise nofile to ${FD_WANT}" >>"${LOG_FILE}"
echo "$(date -Is) run.sh[${IDX}]: nofile soft=$(ulimit -Sn) hard=$(ulimit -Hn) weed=${RUN_WEED}" >>"${LOG_FILE}"

set --
while IFS= read -r line; do
  [ -n "$line" ] && set -- "$@" "$line"
done <"${ARGV_FILE}"

# Supervise rather than exec. DSM's start-stop-status launches this with
# SVC_BACKGROUND=y and then forgets about it -- nothing watches for the process
# dying, so the 2026-08-05 segfault left the volume server down until a human
# noticed 359 under-replicated volumes. Restart it here.
#
# Backoff is capped and the restart is logged with the exit status, so a
# crash-loop is visible in weed.log instead of looking like a healthy service
# that merely restarts a lot. A clean exit (status 0) is treated as an
# intentional stop and is NOT restarted, so `synopkg stop` still works.
CHILD=""
trap 'if [ -n "$CHILD" ]; then kill "$CHILD" 2>/dev/null; fi; exit 0' TERM INT

BACKOFF=1
while :; do
  "${RUN_WEED}" volume "$@" >>"${LOG_FILE}" 2>&1 &
  CHILD=$!
  if wait "$CHILD"; then
    STATUS=0
  else
    STATUS=$?
  fi
  CHILD=""

  if [ "$STATUS" = "0" ]; then
    echo "$(date -Is) run.sh[${IDX}]: weed exited cleanly; not restarting" >>"${LOG_FILE}"
    exit 0
  fi

  echo "$(date -Is) run.sh[${IDX}]: weed died with status ${STATUS}; restarting in ${BACKOFF}s" >>"${LOG_FILE}"
  sleep "$BACKOFF"
  BACKOFF=$((BACKOFF * 2))
  [ "$BACKOFF" -gt 60 ] && BACKOFF=60
done
