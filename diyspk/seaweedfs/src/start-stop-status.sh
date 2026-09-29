#!/bin/sh
# DSM installs conf/systemd units itself. No root-running package hooks or
# writable /etc drop-ins are needed; systemd applies limits before setuid.
set -eu
case "${1:-}" in
    start)
        # DSM must have started our declared dependency before this hook.
        # Fail closed instead of spawning a daemon outside the bounded unit.
        exec /var/packages/seaweedfs/scripts/volume-control status
        ;;
    stop)
        # PartOf ties the system unit to pkgctl stop/restart. This hook only
        # signals processes owned by the package user; it cannot control PID 1.
        exec /var/packages/seaweedfs/scripts/volume-control stop
        ;;
    status|log)
        exec /var/packages/seaweedfs/scripts/volume-control "$1"
        ;;
    *) exit 1 ;;
esac
