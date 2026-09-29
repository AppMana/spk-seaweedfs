#!/bin/sh
set -eu
test -f /root/seaweedfs-private-image
test -f /root/seaweedfs-lab-account-ready
grep -F ' /volume1 btrfs ' /proc/mounts
work=/volume1/seaweedfs-qualification-__RUN__
mkdir -m 700 "$work"
dmesg > "$work/kernel-before.log"
cat /proc/partitions
cat /sys/block/sdb/device/queue_depth
cat /sys/block/sdb/queue/scheduler
payload=__PAYLOAD__
expected=__SHA256__
printf '%s  %s\n' "$expected" "$payload" | sha256sum -c -
# 32 independently verified files, four concurrent writers, fsync each file.
i=0
while [ "$i" -lt 32 ]; do
    pids=''
    j=0
    while [ "$j" -lt 4 ]; do
        dd if="$payload" of="$work/file-$i" bs=1048576 conv=fsync 2>"$work/dd-$i.log" &
        pids="$pids $!"
        i=$((i + 1))
        j=$((j + 1))
    done
    for pid in $pids; do wait "$pid"; done
done
sync
# Drop only this disposable VM's page cache; require fresh filesystem reads.
echo 3 > /proc/sys/vm/drop_caches
i=0
while [ "$i" -lt 32 ]; do
    printf '%s  %s\n' "$expected" "$work/file-$i" | sha256sum -c -
    i=$((i + 1))
done
dmesg > "$work/kernel-after.log"
echo DSM_CHECKSUMS_PASS_32_FILES_256_MIB
if grep -Ei 'failed command:|I/O error|BTRFS.*(error|corrupt)|EXT4-fs error|Buffer I/O' "$work/kernel-after.log"; then
    echo DSM_STORAGE_KERNEL_ERRORS >&2
    exit 1
fi
echo DSM_STORAGE_INTEGRITY_PASS
