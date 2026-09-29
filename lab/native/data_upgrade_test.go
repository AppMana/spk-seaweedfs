package native

import (
	"bytes"
	"context"
	"os"
	"regexp"
	"testing"

	"github.com/appmana/labcontainers/pkg/client"
)

type dsmPackageUpgrade struct {
	baseline, candidate []byte
	version, weedHash   string
}

func TestLiveDSMKubernetesDataUpgrade(t *testing.T) {
	if os.Getenv("DSM_KUBERNETES_INPUTS") == "" {
		t.Skip("requires explicit pinned offline Kubernetes inputs and fresh private DSM disks")
	}
	u := &dsmPackageUpgrade{
		baseline:  pinnedArtifact(t, "DSM_BASELINE_SPK", "DSM_BASELINE_SPK_SHA256"),
		candidate: pinnedArtifact(t, "DSM_CANDIDATE_SPK", "DSM_CANDIDATE_SPK_SHA256"),
		version:   os.Getenv("DSM_BASELINE_VERSION"),
		weedHash:  os.Getenv("DSM_BASELINE_WEED_SHA256"),
	}
	if !regexp.MustCompile(`^[0-9]+(\.[0-9]+)*-[0-9]+$`).MatchString(u.version) || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(u.weedHash) {
		t.Fatal("explicit baseline version and weed SHA256 required")
	}
	if bytes.Equal(u.baseline, u.candidate) {
		t.Fatal("identical baseline and candidate packages do not qualify an upgrade")
	}
	runDSMKubernetesJoin(t, u)
}

func (u *dsmPackageUpgrade) installBaseline(t *testing.T, ctx context.Context, lab *client.Session) {
	t.Helper()
	p := dsmUpload(t, ctx, lab, "data-upgrade-baseline.spk", u.baseline)
	dsmRoot(t, ctx, lab, `set -eu
test -f /root/seaweedfs-private-image
# Reject a used fixture; never uninstall a candidate to manufacture a baseline.
test ! -f /var/packages/seaweedfs/INFO
/usr/syno/bin/synopkg install `+shellQuote(p)+`
grep -Fx `+shellQuote(`version="`+u.version+`"`)+` /var/packages/seaweedfs/INFO
printf '%s  %s\n' `+shellQuote(u.weedHash)+` /var/packages/seaweedfs/target/bin/weed | sha256sum -c -
/usr/syno/bin/synopkg stop seaweedfs
`)
}

func (u *dsmPackageUpgrade) apply(t *testing.T, ctx context.Context, lab *client.Session, version, hash string) {
	t.Helper()
	p := dsmUpload(t, ctx, lab, "data-upgrade-candidate.spk", u.candidate)
	dsmRoot(t, ctx, lab, `set -eu
test -f /root/seaweedfs-private-image
pkg=/var/packages/seaweedfs
manifest=/tmp/seaweedfs-upgrade-`+lab.ID()+`.sha256
sha256sum "$pkg/var/volume.yaml" "$pkg/var/kube/token" "$pkg/var/kube/ca.crt" > "$manifest"
/usr/syno/bin/synopkg install `+shellQuote(p)+`
grep -Fx `+shellQuote(`version="`+version+`"`)+` "$pkg/INFO"
printf '%s  %s\n' `+shellQuote(hash)+` "$pkg/target/bin/weed" | sha256sum -c -
sha256sum -c "$manifest"
# Use the package's normal registration/control boundary; no manual daemon.
sh "$pkg/target/bin/register-service.sh"
/usr/syno/bin/synopkg start seaweedfs
echo DSM_DATA_PACKAGE_UPGRADE_INSTALLED
`)
}
