package native

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"strings"
	"testing"

	"github.com/appmana/labcontainers/pkg/client"
)

//go:embed storage-probe.sh
var storageProbe string

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

// Transport runs in the isolated Linux peer; the script itself runs in DSM.
func dsmUpload(t *testing.T, ctx context.Context, lab *client.Session, name string, data []byte) string {
	t.Helper()
	peer := lab.Node("peer")
	local := "/run/" + name
	remote := "/tmp/seaweedfs-" + lab.ID() + "-" + name
	if err := peer.Put(ctx, local, 0600, data); err != nil {
		t.Fatal(err)
	}
	command := `. /run/dsm-account.env
export SSHPASS="$DSM_PASS"
exec sshpass -e scp -O -p -o ConnectTimeout=5 -o StrictHostKeyChecking=accept-new ` + shellQuote(local) + ` "$DSM_USER@192.0.2.20:` + remote + `"`
	r, err := peer.Exec(ctx, "sh", "-ec", command)
	if err != nil || r.ExitCode != 0 {
		t.Fatalf("DSM upload %s: %v %v", name, r, err)
	}
	return remote
}

func dsmRoot(t *testing.T, ctx context.Context, lab *client.Session, script string) string {
	t.Helper()
	remote := dsmUpload(t, ctx, lab, "probe.sh", []byte(script))
	command := `. /run/dsm-account.env
export SSHPASS="$DSM_PASS"
printf '%s\n' "$DSM_PASS" | sshpass -e ssh -o ConnectTimeout=5 -o StrictHostKeyChecking=accept-new "$DSM_USER@192.0.2.20" ` + shellQuote("sudo -S -p '' /bin/sh "+shellQuote(remote))
	r, err := lab.Node("peer").Exec(ctx, "sh", "-ec", command)
	if r != nil {
		t.Logf("DSM probe stdout:\n%s\nstderr:\n%s", r.Stdout, r.Stderr)
	}
	if err != nil || r.ExitCode != 0 {
		t.Fatalf("DSM probe failed: %v %v", r, err)
	}
	return string(r.Stdout)
}

func TestLiveDSMStorageIntegrity(t *testing.T) {
	runDSM(t, func(ctx context.Context, lab *client.Session) {
		// Independent host oracle, not a checksum learned from guest writes.
		payload := make([]byte, 8<<20)
		if _, err := rand.Read(payload); err != nil {
			t.Fatal(err)
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(payload))
		remote := dsmUpload(t, ctx, lab, "payload.bin", payload)
		script := strings.NewReplacer("__PAYLOAD__", shellQuote(remote), "__SHA256__", digest,
			"__RUN__", lab.ID()).Replace(storageProbe)
		got := dsmRoot(t, ctx, lab, script)
		if !strings.Contains(got, "DSM_STORAGE_INTEGRITY_PASS") {
			t.Fatal("missing actual guest storage completion marker")
		}
	})
}
