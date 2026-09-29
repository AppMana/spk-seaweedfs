#!/bin/sh
# DSM installs conf/systemd units itself. No root-running package hooks or
# writable /etc drop-ins are needed; systemd applies limits before setuid.
set -eu
case "${1:-}" in
    start|stop)
        exec /usr/syno/bin/synosystemctl "$1" pkg-seaweedfs-volume.service
        ;;
    status|log)
        exec /var/packages/seaweedfs/scripts/volume-control "$1"
        ;;
    *) exit 1 ;;
esac
