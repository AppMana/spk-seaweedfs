#!/bin/sh
# RR invokes this only on a private, explicitly marked qualification disk.
set -eu
test -f /root/seaweedfs-private-image
test -f /root/seaweedfs-lab-account.env
. /root/seaweedfs-lab-account.env
case "$DSM_USER" in swlab????????????) ;; *) exit 1 ;; esac
test "$(cat /root/seaweedfs-private-image)" = "$DSM_USER"
test -n "$DSM_PASS"
if /usr/syno/sbin/synouser --get "$DSM_USER" >/dev/null 2>&1; then
  /usr/syno/sbin/synouser --setpw "$DSM_USER" "$DSM_PASS"
else
  /usr/syno/sbin/synouser --add "$DSM_USER" "$DSM_PASS" 'Isolated SeaweedFS lab' 0 lab@example.invalid 1
fi
/usr/syno/sbin/synogroup --memberadd administrators "$DSM_USER"
/usr/syno/bin/synowebapi -s --exec api=SYNO.Core.Terminal method=set version=3 enable_telnet=false enable_ssh=true ssh_port=22 forbid_console=false
systemctl restart sshd
printf '%s\n' 'DSM_PRIVATE_ACCOUNT_READY' > /root/seaweedfs-lab-account-ready
