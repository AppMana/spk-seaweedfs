package native

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/appmana/labcontainers/pkg/client"
)

// Diagnostic: determine whether one-time privileged dependency registration
// permits ordinary Package Center lifecycle control of the unprivileged unit.
// This does not qualify the SPK installation workflow or data recovery.
func TestLiveDSMRegisteredServiceControl(t *testing.T) {
	if os.Getenv("DSM_PRIVATE_DIR") == "" {
		t.Skip("requires explicit prepared private DSM disks")
	}
	candidate := pinnedArtifact(t, "DSM_CANDIDATE_SPK", "DSM_CANDIDATE_SPK_SHA256")
	runDSMFor(t, 20*time.Minute, func(ctx context.Context, lab *client.Session) {
		path := dsmUpload(t, ctx, lab, "registration-candidate.spk", candidate)
		dsmRoot(t, ctx, lab, `set -eu
test -f /root/seaweedfs-private-image
/usr/syno/bin/synopkg stop seaweedfs
/usr/syno/bin/synopkg install `+shellQuote(path)+`
/usr/syno/bin/synopkg stop seaweedfs
systemctl disable pkg-seaweedfs-volume.service
rm -f /etc/systemd/system/pkg-seaweedfs-volume.service.d/lab-ordering.conf
systemctl daemon-reload
test ! -e /etc/systemd/system/pkgctl-seaweedfs.service.requires/pkg-seaweedfs-volume.service
sh /var/packages/seaweedfs/target/bin/register-service.sh
sh /var/packages/seaweedfs/target/bin/register-service.sh
/usr/syno/bin/synopkg start seaweedfs
systemctl is-active pkg-seaweedfs-volume.service
/usr/syno/bin/synopkg status seaweedfs
/usr/syno/bin/synopkg stop seaweedfs
`)
		assertDSMStopped(t, ctx, lab)
		t.Log("DSM_REGISTERED_SERVICE_CONTROL_PASS")
	})
}

// Read the installed DSM unit integration rules before choosing a package
// activation mechanism. This diagnostic is not an installation pass.
func TestLiveDSMUnitInventory(t *testing.T) {
	runDSM(t, func(ctx context.Context, lab *client.Session) {
		dsmRoot(t, ctx, lab, `set -eu
test -f /root/seaweedfs-private-image
systemctl cat pkgctl-seaweedfs.service
systemctl cat seaweedfs.slice
for folder in /usr/local/lib/systemd/system /usr/syno/lib/systemd/system /lib/systemd/system; do
  test -d "$folder" || continue
  grep -l 'controlled-by\|start-on' "$folder"/*.service 2>/dev/null | head -20 | while read -r unit; do
    echo "UNIT_RULES $unit"
    grep -E '^\[|^start-on=|^controlled-by=|^Requires=|^Wants=|^After=|^Before=|^WantedBy=' "$unit"
  done
done
/usr/syno/sbin/synopkgctl --help || true
/usr/syno/bin/synosystemctl --help || true
echo DSM_UNIT_INVENTORY_COMPLETE
`)
	})
}

// Diagnostic evidence only: this does not qualify package resource isolation.
func TestLiveDSMResourceCapabilities(t *testing.T) {
	runDSM(t, func(ctx context.Context, lab *client.Session) {
		dsmRoot(t, ctx, lab, `#!/bin/sh
set -eu
test -f /root/seaweedfs-private-image
systemctl --version
cat /proc/cgroups
grep cgroup /proc/mounts
systemctl show pkgctl-seaweedfs.service -p FragmentPath -p DropInPaths -p Slice -p LimitNOFILE -p LimitNPROC -p MemoryAccounting -p MemoryLimit -p MemoryMax -p TasksMax -p User -p Group
systemctl cat pkgctl-seaweedfs.service
systemctl cat seaweedfs.slice || true
/usr/syno/bin/synosystemctl --help || true
find /var/packages/seaweedfs/conf -maxdepth 2 -type f
ls -ld /var/packages/seaweedfs /var/packages/seaweedfs/target /var/packages/seaweedfs/var
readlink -f /var/packages/seaweedfs/target || true
ls -la /var/packages/seaweedfs/target/ || true
ls -la /var/packages/seaweedfs/target/bin/ || true
ls -la /volume1/@appstore/seaweedfs/ || true
grep -E 'volume1|loop' /proc/mounts
/usr/syno/bin/synopkg status seaweedfs || true
tail -60 /var/log/packages/seaweedfs.log || true
echo DSM_RESOURCE_DIAGNOSTICS_COMPLETE
`)
	})
}

// Reduce package startup failures to the actual DSM service-control boundary;
// no Kubernetes backend or data workload is needed for this diagnostic.
func TestLiveDSMServiceControl(t *testing.T) {
	runDSM(t, func(ctx context.Context, lab *client.Session) {
		dsmRoot(t, ctx, lab, `set -u
test -f /root/seaweedfs-private-image || exit 1
ls -l /usr/syno/bin/synosystemctl
for unit in pkg-seaweedfs-volume.service pkg-seaweedfs-volume; do
  printf 'ROOT_LOAD unit=%s\n' "$unit"
  /usr/syno/bin/synosystemctl get-load-status "$unit"; printf 'exit=%s\n' "$?"
  printf 'PACKAGE_LOAD unit=%s\n' "$unit"
  su -s /bin/sh sc-seaweedfs -c "/usr/syno/bin/synosystemctl get-load-status $unit"; printf 'exit=%s\n' "$?"
done
echo PACKAGE_START
su -s /bin/sh sc-seaweedfs -c '/usr/syno/bin/synosystemctl start pkg-seaweedfs-volume.service'; printf 'exit=%s\n' "$?"
echo ROOT_START
/usr/syno/bin/synosystemctl start pkg-seaweedfs-volume.service; printf 'exit=%s\n' "$?"
systemctl status pkg-seaweedfs-volume.service --no-pager || true
journalctl -u pkg-seaweedfs-volume.service --no-pager -n 40 || true
/usr/syno/bin/synosystemctl stop pkg-seaweedfs-volume.service; printf 'stop_exit=%s\n' "$?"
echo DSM_SERVICE_CONTROL_DIAGNOSTICS_COMPLETE
`)
	})
}
