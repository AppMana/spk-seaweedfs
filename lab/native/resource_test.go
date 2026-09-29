package native

import (
	"context"
	"testing"

	"github.com/appmana/labcontainers/pkg/client"
)

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
