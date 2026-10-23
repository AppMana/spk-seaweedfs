package native

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/appmana/labcontainers/pkg/client"
)

// The DSM seed already contains an older package. Uninstalling alone is not
// a fresh-install test because DSM retains @appdata. Archive that lab-only
// state, without deleting it, and assert the installer creates new state.
// CLI installation does not supply wizard credentials: authenticated cluster
// configuration and discovery are verified by TestLiveDSMKubernetesJoin.
func TestLiveDSMPackageFreshInstall(t *testing.T) {
	if os.Getenv("DSM_PRIVATE_DIR") == "" {
		t.Skip("requires explicit prepared private DSM disks")
	}
	candidate := pinnedArtifact(t, "DSM_CANDIDATE_SPK", "DSM_CANDIDATE_SPK_SHA256")
	runDSMFor(t, 20*time.Minute, func(ctx context.Context, lab *client.Session) {
		qualifyDSMFreshInstall(t, ctx, lab, candidate)
	})
}

func qualifyDSMFreshInstall(t *testing.T, ctx context.Context, lab *client.Session, candidate []byte) {
	t.Helper()
	path := dsmUpload(t, ctx, lab, "fresh-candidate.spk", candidate)
	dsmRoot(t, ctx, lab, `set -eu
test -f /root/seaweedfs-private-image
test -f /root/seaweedfs-lab-account-ready
pkg=/var/packages/seaweedfs
test -f "$pkg/INFO"
/usr/syno/bin/synopkg stop seaweedfs
state=$(readlink -f "$pkg/var")
case "$state" in /volume[0-9]*/@appdata/seaweedfs) ;; *) exit 1;; esac
backup="$state.before-fresh-`+lab.ID()+`"
test ! -e "$backup"
shares_before=$(/usr/syno/sbin/synoshare --enum ALL | sed '1,2d' | sort)
/usr/syno/bin/synopkg uninstall seaweedfs
test ! -f "$pkg/INFO"
test ! -e "$pkg/target"
test -d "$state"
mv "$state" "$backup"
test ! -e "$state"
/usr/syno/bin/synopkg install `+shellQuote(path)+`
test "$(readlink -f "$pkg/var")" = "$state"
test -s "$state/volume.yaml"
test -s "$state/volume_template.yaml"
test -f "$state/kube/token"
test -f "$state/kube/ca.crt"
# The template's explanatory comment intentionally says @PLACEHOLDERS@;
# only live configuration lines are substitution failures.
if grep -v '^[[:space:]]*#' "$state/volume.yaml" | grep -Eq '@[A-Z_]+@'; then
  echo 'fresh installer left unresolved template fields' >&2
  exit 1
fi
uid=$(id -u sc-seaweedfs)
for file in "$state/volume.yaml" "$state/kube/token"; do
  test "$(stat -c %a "$file")" = 600
  test "$(stat -c %u "$file")" = "$uid"
done
test -d "$backup"
# The default server is an @ directory on the lowest-numbered volume, which
# DSM never manages as a shared folder; the package creates no share.
first=$(awk '$2 ~ /^\/volume[0-9]+$/ { sub(/^\/volume/, "", $2); print $2 }' /proc/mounts | sort -n | head -n 1)
data=/volume$first/@seaweedfs
grep -qx "  dir: $data" "$state/volume.yaml"
shares_after=$(/usr/syno/sbin/synoshare --enum ALL | sed '1,2d' | sort)
if [ "$shares_after" != "$shares_before" ]; then
  echo "fresh install changed DSM shared folders: before [$shares_before] after [$shares_after]" >&2
  exit 1
fi
sh "$pkg/target/bin/register-service.sh"
test -d "$data"
test ! -L "$data"
test "$(stat -c %u "$data")" = "$uid"
/usr/syno/sbin/synoshare --enum ALL | sed '1,2d' | while read -r share; do
  /usr/syno/sbin/synoshare --get "$share" | grep -q "Path \.*\[$data\]" && { echo "$data is a shared folder" >&2; exit 1; }
done
echo DSM_PACKAGE_FRESH_INSTALL_PASS
`)
}
