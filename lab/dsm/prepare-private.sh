#!/usr/bin/env bash
# Prepare private DSM disks only; never boot or attach a network here.
set -euo pipefail
umask 077
if [[ $# != 3 ]]; then
  echo 'usage: prepare-private.sh RR_SEED DSM_SEED OUTPUT_PARENT' >&2
  exit 2
fi
rr_seed=$(realpath -e -- "$1")
dsm_seed=$(realpath -e -- "$2")
output_parent=$(realpath -e -- "$3")
[[ -f "$rr_seed" && -f "$dsm_seed" && -d "$output_parent" ]]
script_dir=$(cd -- "$(dirname -- "$0")" && pwd)
run_dir=$(mktemp -d "$output_parent/dsm-private.XXXXXXXX")
# Full independent copies (reflinks are copy-on-write, not backing files).
sha256sum -- "$rr_seed" "$dsm_seed" > "$run_dir/seeds.sha256"
cp --reflink=auto --sparse=always -- "$rr_seed" "$run_dir/rr.img"
cp --reflink=auto --sparse=always -- "$dsm_seed" "$run_dir/data.qcow2"
chmod 600 "$run_dir/rr.img" "$run_dir/data.qcow2"
account="swlab$(openssl rand -hex 6)"
password=$(openssl rand -hex 24)
printf 'DSM_USER=%s\nDSM_PASS=%s\n' "$account" "$password" > "$run_dir/account.env"
unset password
printf '%s\n' "$account" > "$run_dir/private-image-marker"
guestfish=(sudo -n guestfish --format=qcow2 -a "$run_dir/data.qcow2")
# Detect the system RAID member by its DSM VERSION file, never assume md order.
root_device=''
while IFS= read -r device; do
  [[ "$device" =~ ^/dev/md[0-9]+$ ]] || continue
  kind=$("${guestfish[@]}" --ro run : vfs-type "$device")
  [[ "$kind" == ext4 ]] || continue
  marker=$("${guestfish[@]}" --ro run : mount-ro "$device" / : is-file /etc.defaults/VERSION)
  if [[ "$marker" == true ]]; then
    [[ -z "$root_device" ]] || { echo 'ambiguous DSM root' >&2; exit 1; }
    root_device=$device
  fi
done < <("${guestfish[@]}" --ro run : list-md-devices)
[[ -n "$root_device" ]] || { echo 'no DSM system filesystem' >&2; exit 1; }
"${guestfish[@]}" run : mount "$root_device" / \
  : mkdir-p /usr/rr/once.d \
  : upload "$run_dir/account.env" /root/seaweedfs-lab-account.env \
  : chmod 0600 /root/seaweedfs-lab-account.env \
  : upload "$run_dir/private-image-marker" /root/seaweedfs-private-image \
  : chmod 0600 /root/seaweedfs-private-image \
  : upload "$script_dir/private-account-hook.sh" /usr/rr/once.d/00-seaweedfs-lab-account.sh \
  : chmod 0700 /usr/rr/once.d/00-seaweedfs-lab-account.sh \
  : mkdir-p /usr/local/etc/rc.d \
  : upload "$script_dir/private-network.sh" /usr/local/etc/rc.d/seaweedfs-lab-network.sh \
  : chmod 0700 /usr/local/etc/rc.d/seaweedfs-lab-network.sh \
  : sync
sha256sum --check --status "$run_dir/seeds.sha256"
printf 'DSM_PRIVATE_IMAGE_PREPARED:%s\n' "$run_dir"
