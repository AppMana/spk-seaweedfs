package native

import (
	"bytes"
	"context"
	"errors"
	"os"
	"regexp"
	"testing"

	"github.com/appmana/labcontainers/pkg/client"
)

type dsmPackageUpgrade struct {
	baseline, candidate []byte
	version, weedHash   string
	seedVersion         string
	packageOnly         bool
}

// dsmUpgradePair checks the baseline/candidate identities the upgrade test
// is given. By default the upgrade swaps the weed binary under existing data.
// A package-only upgrade (DSM_PACKAGE_ONLY_UPGRADE=1) changes only packaging
// (wizard, service scripts, registration) and must keep the very same weed;
// it is chosen explicitly, never inferred from equal hashes.
func dsmUpgradePair(baseVersion, baseWeed, candVersion, candWeed string, packageOnly bool) error {
	if baseVersion == candVersion {
		return errors.New("upgrade requires distinct baseline/candidate package versions")
	}
	if packageOnly && baseWeed != candWeed {
		return errors.New("package-only upgrade requires the identical weed binary")
	}
	if !packageOnly && baseWeed == candWeed {
		return errors.New("data upgrade requires distinct baseline/candidate package versions and binaries")
	}
	return nil
}

func TestLiveDSMKubernetesDataUpgrade(t *testing.T) {
	if os.Getenv("DSM_KUBERNETES_INPUTS") == "" {
		t.Skip("requires explicit pinned offline Kubernetes inputs and fresh private DSM disks")
	}
	runDSMKubernetesJoin(t, dsmUpgradeFromEnv(t), nil)
}

func dsmUpgradeFromEnv(t *testing.T) *dsmPackageUpgrade {
	t.Helper()
	u := &dsmPackageUpgrade{
		baseline:    pinnedArtifact(t, "DSM_BASELINE_SPK", "DSM_BASELINE_SPK_SHA256"),
		candidate:   pinnedArtifact(t, "DSM_CANDIDATE_SPK", "DSM_CANDIDATE_SPK_SHA256"),
		version:     os.Getenv("DSM_BASELINE_VERSION"),
		weedHash:    os.Getenv("DSM_BASELINE_WEED_SHA256"),
		seedVersion: os.Getenv("DSM_SEED_PACKAGE_VERSION"),
	}
	switch mode := os.Getenv("DSM_PACKAGE_ONLY_UPGRADE"); mode {
	case "":
	case "1":
		u.packageOnly = true
		t.Log("DSM_PACKAGE_ONLY_UPGRADE: candidate must keep the baseline's weed binary")
	default:
		t.Fatalf("DSM_PACKAGE_ONLY_UPGRADE must be unset or 1, got %q", mode)
	}
	if !regexp.MustCompile(`^[0-9]+(\.[0-9]+)*-[0-9]+$`).MatchString(u.version) || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(u.weedHash) {
		t.Fatal("explicit baseline version and weed SHA256 required")
	}
	if bytes.Equal(u.baseline, u.candidate) {
		t.Fatal("identical baseline and candidate packages do not qualify an upgrade")
	}
	if u.seedVersion != "" && !regexp.MustCompile(`^[0-9]+(\.[0-9]+)*-[0-9]+$`).MatchString(u.seedVersion) {
		t.Fatal("invalid explicit seed package version")
	}
	return u
}

func (u *dsmPackageUpgrade) installBaseline(t *testing.T, ctx context.Context, lab *client.Session) {
	t.Helper()
	p := dsmUpload(t, ctx, lab, "data-upgrade-baseline.spk", u.baseline)
	dsmRoot(t, ctx, lab, `set -eu
test -f /root/seaweedfs-private-image
# The immutable DSM seed can contain an older package. Require its explicit
# expected identity; never uninstall a candidate to manufacture a baseline.
if test -f /var/packages/seaweedfs/INFO; then
  grep -Fx `+shellQuote(`version="`+u.seedVersion+`"`)+` /var/packages/seaweedfs/INFO
fi
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

// A binary-swap upgrade must change weed; a package-only upgrade (wizard,
// service scripts, registration) must keep the very same weed and still be a
// different package version. Neither mode is inferred from the inputs.
func TestDSMUpgradePairModes(t *testing.T) {
	const weed248, weed247 = "4ff45232eee7328343075e7dee41984803f156bf77b7d4e7f5656d89cfc1010e", "1fdc61adc091c4ea69c643d38dd3211c8c41c9b8c007f1f0bb31a4e63f8aeb7b"
	for _, tc := range []struct {
		name                    string
		baseVer, baseWeed       string
		candVer, candWeed       string
		packageOnly, acceptable bool
	}{
		{"binary swap", "4.47-15", weed247, "4.48-5", weed248, false, true},
		{"binary mode refuses the same weed", "4.48-2", weed248, "4.48-5", weed248, false, false},
		{"binary mode refuses the same version", "4.48-5", weed247, "4.48-5", weed248, false, false},
		{"package-only 4.48-2 to 4.48-5", "4.48-2", weed248, "4.48-5", weed248, true, true},
		{"package-only refuses a changed weed", "4.47-15", weed247, "4.48-5", weed248, true, false},
		{"package-only refuses the same version", "4.48-5", weed248, "4.48-5", weed248, true, false},
	} {
		err := dsmUpgradePair(tc.baseVer, tc.baseWeed, tc.candVer, tc.candWeed, tc.packageOnly)
		if (err == nil) != tc.acceptable {
			t.Errorf("%s: err=%v, acceptable=%v", tc.name, err, tc.acceptable)
		}
	}
}
