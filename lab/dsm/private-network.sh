#!/bin/sh
# Executed only inside the explicitly marked disposable DSM image.
set -eu
test -f /root/seaweedfs-private-image
case "${1:-start}" in stop) exit 0;; start) ;; *) exit 2;; esac
test -d /sys/class/net/eth0
ip link set eth0 up
ip -4 addr replace 192.0.2.20/24 dev eth0
ip -4 route flush default
ip -6 route flush default
