package native

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/appmana/labcontainers/pkg/client"
)

// This destructive package lifecycle gate is restricted to prepared private
// guests. It never deletes volume data, restores a backup, or recreates the
// configuration/token to make reinstall pass. HTTP data integrity is a
// separate Kubernetes gate; these sentinel files test installer preservation.
func TestLiveDSMPackageUninstallRetention(t *testing.T) {
	if os.Getenv("DSM_PRIVATE_DIR") == "" {
		t.Skip("requires explicit prepared private DSM disks")
	}
	candidate := pinnedArtifact(t, "DSM_CANDIDATE_SPK", "DSM_CANDIDATE_SPK_SHA256")
	runDSMFor(t, 20*time.Minute, func(ctx context.Context, lab *client.Session) {
		qualifyDSMRetention(t, ctx, lab, candidate)
	})
}

func qualifyDSMRetention(t *testing.T, ctx context.Context, lab *client.Session, candidate []byte) {
	t.Helper()
	path := dsmUpload(t, ctx, lab, "retention-candidate.spk", candidate)
	dsmRoot(t, ctx, lab, `set -eu
test -f /root/seaweedfs-private-image
test -f /root/seaweedfs-lab-account-ready
pkg=/var/packages/seaweedfs
test -s "$pkg/INFO"
/usr/syno/bin/synopkg stop seaweedfs
state=$(readlink -f "$pkg/var")
case "$state" in /volume[0-9]*/@appdata/seaweedfs) ;; *) echo 'unexpected package state directory' >&2; exit 1;; esac
test -s "$state/volume.yaml"
test -f "$state/kube/token"
manifest=/tmp/seaweedfs-retention-`+lab.ID()+`.sha256
sha256sum "$state/volume.yaml" "$state/kube/token" "$state/kube/ca.crt" > "$manifest"
data=/volume1/seaweedfs-retention-`+lab.ID()+`
test ! -e "$data"
install -d -m 700 -o sc-seaweedfs "$data"
printf 'retained data, not a SeaweedFS volume: %s\n' `+shellQuote(lab.ID())+` > "$data/sentinel"
chown sc-seaweedfs "$data/sentinel"
sha256sum "$data/sentinel" >> "$manifest"
/usr/syno/bin/synopkg uninstall seaweedfs
test ! -f "$pkg/INFO"
sha256sum -c "$manifest"
echo DSM_UNINSTALL_STATE_RETAINED
/usr/syno/bin/synopkg install `+shellQuote(path)+`
test "$(readlink -f "$pkg/var")" = "$state"
sha256sum -c "$manifest"
test "$(stat -c %a "$state/volume.yaml")" = 600
test "$(stat -c %a "$state/kube/token")" = 600
uid=$(id -u sc-seaweedfs)
for file in "$state/volume.yaml" "$state/kube/token" "$data/sentinel"; do
  test "$(stat -c %u "$file")" = "$uid"
done
sh "$pkg/target/bin/register-service.sh"
/usr/syno/bin/synopkg start seaweedfs
/usr/syno/bin/synopkg status seaweedfs
/usr/syno/bin/synopkg stop seaweedfs
echo DSM_PACKAGE_UNINSTALL_REINSTALL_RETENTION_PASS
`)
	assertDSMStopped(t, ctx, lab)
}
