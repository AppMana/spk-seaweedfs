package native

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/appmana/labcontainers/pkg/client"
)

func pinnedArtifact(t *testing.T, pathKey, hashKey string) []byte {
	t.Helper()
	path, expected := os.Getenv(pathKey), os.Getenv(hashKey)
	if path == "" || len(expected) != 64 {
		t.Fatalf("explicit %s and SHA256 %s required", pathKey, hashKey)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	actual := fmt.Sprintf("%x", sha256.Sum256(data))
	if actual != expected {
		t.Fatalf("%s: SHA256 %s does not match %s", pathKey, actual, expected)
	}
	t.Logf("artifact %s SHA256=%s", path, actual)
	return data
}

// This gate tests DSM's actual installer and upgrade hooks. Cluster membership,
// volume migration and crash recovery are separate gates, not inferred here.
func TestLiveDSMPackageInstallUpgrade(t *testing.T) {
	if os.Getenv("DSM_PRIVATE_DIR") == "" {
		t.Skip("requires explicit prepared private DSM disks")
	}
	baseline := pinnedArtifact(t, "DSM_BASELINE_SPK", "DSM_BASELINE_SPK_SHA256")
	candidate := pinnedArtifact(t, "DSM_CANDIDATE_SPK", "DSM_CANDIDATE_SPK_SHA256")
	baselineVersion, candidateVersion := os.Getenv("DSM_BASELINE_VERSION"), os.Getenv("DSM_CANDIDATE_VERSION")
	versionPattern := regexp.MustCompile(`^[0-9]+(\.[0-9]+)*-[0-9]+$`)
	if !versionPattern.MatchString(baselineVersion) || !versionPattern.MatchString(candidateVersion) {
		t.Fatal("explicit DSM_BASELINE_VERSION and DSM_CANDIDATE_VERSION required")
	}
	weedHash := os.Getenv("DSM_CANDIDATE_WEED_SHA256")
	if len(weedHash) != 64 || strings.Trim(weedHash, "0123456789abcdef") != "" {
		t.Fatal("explicit DSM_CANDIDATE_WEED_SHA256 required")
	}
	runDSMFor(t, 20*time.Minute, func(ctx context.Context, lab *client.Session) {
		basePath := dsmUpload(t, ctx, lab, "baseline.spk", baseline)
		candidatePath := dsmUpload(t, ctx, lab, "candidate.spk", candidate)
		script := `#!/bin/sh
set -eu
test -f /root/seaweedfs-private-image
test -f /root/seaweedfs-lab-account-ready
pkg=/var/packages/seaweedfs
echo DSM_PACKAGE_BEFORE
if test -f "$pkg/INFO"; then grep -E '^(package|version)=' "$pkg/INFO"; fi
systemctl --version
/usr/syno/bin/synopkg install __BASELINE__
grep -Fx __BASELINE_VERSION__ "$pkg/INFO"
/usr/syno/bin/synopkg stop seaweedfs
test -s "$pkg/var/volume.yaml"
# This comment is a configuration-retention oracle, not replacement config.
printf '\n# qualification-retention-__RUN__\n' >> "$pkg/var/volume.yaml"
sha256sum "$pkg/var/volume.yaml" > /tmp/seaweedfs-__RUN__-config.sha256
stat -c '%u:%g:%a' "$pkg/var/volume.yaml" > /tmp/seaweedfs-__RUN__-config.stat
echo DSM_PACKAGE_BASELINE_INSTALLED
/usr/syno/bin/synopkg install __CANDIDATE__
grep -Fx __CANDIDATE_VERSION__ "$pkg/INFO"
/usr/syno/bin/synopkg stop seaweedfs
sha256sum -c /tmp/seaweedfs-__RUN__-config.sha256
test "$(stat -c '%u:%g:%a' "$pkg/var/volume.yaml")" = "$(cat /tmp/seaweedfs-__RUN__-config.stat)"
printf '%s  %s\n' __WEED_SHA__ "$pkg/target/bin/weed" | sha256sum -c -
"$pkg/target/bin/weed" version
unit=pkg-seaweedfs-volume.service
cmp "$pkg/conf/systemd/$unit" "/usr/local/lib/systemd/system/$unit"
# DSM systemd 219 uses MemoryLimit and per-UID RLIMIT_NPROC. The previous
# drop-in's MemoryMax/TasksMax names are unsupported there, not protection.
systemctl show "$unit" -p LimitNOFILE -p MemoryLimit -p LimitNPROC -p User -p Group > /tmp/seaweedfs-__RUN__-limits
cat /tmp/seaweedfs-__RUN__-limits
grep -Fx 'LimitNOFILE=65536' /tmp/seaweedfs-__RUN__-limits
grep -Fx 'MemoryLimit=5368709120' /tmp/seaweedfs-__RUN__-limits
grep -Fx 'LimitNPROC=4096' /tmp/seaweedfs-__RUN__-limits
grep -Fx 'User=sc-seaweedfs' /tmp/seaweedfs-__RUN__-limits
grep -Fx 'Group=synocommunity' /tmp/seaweedfs-__RUN__-limits
id sc-seaweedfs
echo DSM_PACKAGE_INSTALL_UPGRADE_PASS
`
		script = strings.NewReplacer("__BASELINE__", shellQuote(basePath), "__CANDIDATE__", shellQuote(candidatePath),
			"__BASELINE_VERSION__", shellQuote(`version="`+baselineVersion+`"`),
			"__CANDIDATE_VERSION__", shellQuote(`version="`+candidateVersion+`"`),
			"__RUN__", lab.ID(), "__WEED_SHA__", shellQuote(weedHash)).Replace(script)
		got := dsmRoot(t, ctx, lab, script)
		if !strings.Contains(got, "DSM_PACKAGE_INSTALL_UPGRADE_PASS") {
			t.Fatal("missing actual guest install/upgrade completion marker")
		}
	})
}
