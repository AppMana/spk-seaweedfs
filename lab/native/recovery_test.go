package native

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/appmana/labcontainers/pkg/client"
)

// Only write before the first verification. Restart/recovery never reseeds
// payloads, creates new FIDs, reinstalls packages, or repairs the mount/service.
func verifyDSMDataRecovery(t *testing.T, ctx context.Context, lab *client.Session) {
	verifyDSMDataRecoveryAcross(t, ctx, lab, nil)
}

// transition is an actual package upgrade between original-data verification
// and readback. It must not create, replace or repair any workload objects.
func verifyDSMDataRecoveryAcross(t *testing.T, ctx context.Context, lab *client.Session, transition func()) {
	t.Helper()
	defer func() {
		if !t.Failed() {
			return
		}
		// Retain guest boot/service evidence before lab teardown. This is
		// read-only: never restart or repair a failed recovery to pass it.
		debug, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		dsmRoot(t, debug, lab, `set -u
systemctl show pkg-volume.target pkg-seaweedfs-volume.service -p Id -p ActiveEnterTimestamp -p ExecMainStartTimestamp -p ActiveState -p Result
journalctl -b -u pkg-seaweedfs-volume.service --no-pager -n 60
tail -40 /var/packages/seaweedfs/var/log/weed.log
sha256sum /var/packages/seaweedfs/var/volume.yaml /var/packages/seaweedfs/var/kube/token /var/packages/seaweedfs/var/kube/ca.crt
`)
	}()
	// Use the isolated tools container for the HTTP workload. DSM and the
	// Kubernetes backend remain real VMs; only the client probes avoid QGA.
	workload := lab.Node("peer")
	run := func(args ...string) []byte {
		t.Helper()
		out, err := workload.Commands().Exec(ctx, args...)
		if err != nil {
			t.Fatalf("data probe %v: %s: %v", args, out, err)
		}
		return out
	}
	payload := make([]byte, 2<<20)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256(payload))
	if err := workload.Put(ctx, "/run/qualification-data.bin", 0600, payload); err != nil {
		t.Fatal(err)
	}
	var assignment struct {
		FID   string `json:"fid"`
		URL   string `json:"url"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(run("curl", "--max-time", "30", "-fsS", "http://192.0.2.30:9333/dir/assign?dataCenter=lab&rack=synology"), &assignment); err != nil {
		t.Fatal(err)
	}
	if assignment.Error != "" || assignment.FID == "" || assignment.URL != "192.0.2.20:8080" {
		t.Fatalf("assignment did not target DSM: %+v", assignment)
	}
	rawURL := "http://" + assignment.URL + "/" + assignment.FID
	filerURL := "http://192.0.2.30:8888/lab/" + lab.ID() + "/payload.bin"
	run("curl", "--max-time", "30", "-fsS", "-F", "file=@/run/qualification-data.bin", rawURL+"?fsync=true")
	run("curl", "--max-time", "30", "-fsS", "-F", "file=@/run/qualification-data.bin", filerURL+"?fsync=true&dataCenter=lab&rack=synology")
	t.Logf("acknowledged DSM dataset raw_fid=%s filer=%s bytes=%d sha256=%s", assignment.FID, filerURL, len(payload), want)
	verify := func(phase string) {
		t.Helper()
		for _, url := range []string{rawURL, filerURL} {
			run("curl", "--max-time", "30", "-fsS", "-o", "/run/qualification-read.bin", url)
			got := strings.Fields(string(run("sha256sum", "/run/qualification-read.bin")))
			if len(got) != 2 || got[0] != want {
				t.Fatalf("%s data mismatch for %s: %v", phase, url, got)
			}
		}
		t.Logf("DSM_DATA_PERSISTENCE_PASS phase=%s original_fid=%s sha256=%s", phase, assignment.FID, want)
	}
	ready := func() {
		t.Helper()
		limit, cancel := context.WithTimeout(ctx, 6*time.Minute)
		defer cancel()
		for {
			// The original acknowledged object, not merely an open HTTP port.
			result, err := workload.ExecWithTimeout(limit, 10*time.Second, "curl", "--max-time", "8", "-fsS", "-o", "/run/recovery-ready.bin", rawURL)
			if err == nil && result.ExitCode == 0 {
				return
			}
			select {
			case <-limit.Done():
				t.Fatalf("DSM original object did not recover: result=%+v error=%v", result, err)
			case <-time.After(3 * time.Second):
			}
		}
	}
	verify("before-restart")
	if transition != nil {
		transition()
		ready()
		verify("after-package-upgrade")
	}
	dsmRoot(t, ctx, lab, "set -eu\ntest -f /root/seaweedfs-private-image\n/usr/syno/bin/synopkg restart seaweedfs\n")
	ready()
	verify("after-package-restart")
	configHash := strings.TrimSpace(dsmRoot(t, ctx, lab, "sha256sum /var/packages/seaweedfs/var/volume.yaml /var/packages/seaweedfs/var/kube/token /var/packages/seaweedfs/var/kube/ca.crt"))
	boot := strings.TrimSpace(dsmRoot(t, ctx, lab, "cat /proc/sys/kernel/random/boot_id"))
	if len(boot) != 36 {
		t.Fatalf("invalid pre-crash boot identity %q", boot)
	}
	if err := lab.Node("dsm").Crash(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := workload.ExecWithTimeout(ctx, 5*time.Second, "curl", "--max-time", "3", "-fsS", rawURL)
	if err != nil {
		t.Fatal("outage probe transport failed:", err)
	}
	if result.ExitCode != 7 && result.ExitCode != 28 {
		t.Fatalf("expected network outage after crash, curl exit=%d", result.ExitCode)
	}
	if err := lab.Node("dsm").Start(ctx); err != nil {
		t.Fatal(err)
	}
	ready()
	after := strings.TrimSpace(dsmRoot(t, ctx, lab, "cat /proc/sys/kernel/random/boot_id"))
	if len(after) != 36 || after == boot {
		t.Fatalf("crash did not yield new guest boot: %q -> %q", boot, after)
	}
	if recovered := strings.TrimSpace(dsmRoot(t, ctx, lab, "sha256sum /var/packages/seaweedfs/var/volume.yaml /var/packages/seaweedfs/var/kube/token /var/packages/seaweedfs/var/kube/ca.crt")); recovered != configHash {
		t.Fatalf("provisioned configuration changed across power loss: before=%s after=%s", configHash, recovered)
	}
	verify("after-abrupt-vm-crash")
	dsmRoot(t, ctx, lab, `set -eu
test -f /root/seaweedfs-private-image
if dmesg | grep -E 'failed command:|I/O error|BTRFS.*(error|corrupt)|EXT4-fs error|Buffer I/O'; then
  echo DSM_STORAGE_HEALTH_FAILED >&2
  exit 1
fi
echo DSM_POST_CRASH_STORAGE_HEALTH_PASS
`)
}
