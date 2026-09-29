package native

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	labv1 "github.com/appmana/labcontainers/api/v1"
	"github.com/appmana/labcontainers/pkg/client"
	clab "github.com/appmana/labcontainers/pkg/containerlab"
	"github.com/srl-labs/containerlab/core"
	"github.com/srl-labs/containerlab/links"
	"github.com/srl-labs/containerlab/types"
)

func dsmTopology(private, image, peerImage string) (*labv1.TopologySource, error) {
	config, err := dsmConfig(private, image, peerImage)
	if err != nil {
		return nil, err
	}
	return clab.Source(config)
}

func dsmConfig(private, image, peerImage string) (*core.Config, error) {
	if !filepath.IsAbs(private) || !strings.HasPrefix(filepath.Base(private), "dsm-private.") {
		return nil, fmt.Errorf("explicit prepared private-image directory required")
	}
	return &core.Config{Topology: &types.Topology{
		Defaults: &types.NodeDefinition{NetworkMode: "none", ImagePullPolicy: "Never"},
		Nodes: map[string]*types.NodeDefinition{
			"dsm":  {Kind: "generic_vm", Image: image, Binds: []string{private + ":/private"}, Env: map[string]string{"DSM_QEMU_TRACE": os.Getenv("DSM_QEMU_TRACE")}},
			"peer": {Kind: "linux", Image: peerImage, Entrypoint: "/bin/sleep", Cmd: "infinity"},
		},
		Links: []*links.LinkDefinition{{Link: &links.LinkBriefRaw{Endpoints: []string{"dsm:eth1", "peer:eth1"}}}},
	}}, nil
}

func TestDSMTopologyRejectsSeedDirectory(t *testing.T) {
	for _, path := range []string{"", "/", "../out", "/home/user/lab/dsm/out"} {
		if _, err := dsmTopology(path, "dsm:pinned", "peer:pinned"); err == nil {
			t.Fatal("accepted non-private path", path)
		}
	}
}

func TestLiveDSMPrivateBoot(t *testing.T) {
	runDSM(t, nil)
}

func runDSM(t *testing.T, afterBoot func(context.Context, *client.Session)) {
	runDSMFor(t, 10*time.Minute, afterBoot)
}

func runDSMFor(t *testing.T, budget time.Duration, afterBoot func(context.Context, *client.Session)) {
	runDSMScenario(t, budget, nil, afterBoot)
}

// A scenario extends native objects before launch; it cannot silently attach
// the guest or host to a management/WAN network.
type dsmScenario func(*core.Config, *labv1.LabSpec) (peerSetup string, err error)

func runDSMScenario(t *testing.T, budget time.Duration, scenario dsmScenario, afterBoot func(context.Context, *client.Session)) {
	t.Helper()
	private := os.Getenv("DSM_PRIVATE_DIR")
	if private == "" {
		t.Skip("requires explicit prepared private DSM disks")
	}
	image, peerImage, daemon := os.Getenv("DSM_VM_IMAGE"), os.Getenv("DSM_PEER_IMAGE"), os.Getenv("LABCONTAINERS_LABD")
	if image == "" || peerImage == "" || daemon == "" {
		t.Fatal("pinned images and matched daemon required")
	}
	credentials, err := os.ReadFile(filepath.Join(private, "account.env"))
	if err != nil {
		t.Fatal(err)
	}
	config, err := dsmConfig(private, image, peerImage)
	if err != nil {
		t.Fatal(err)
	}
	spec := &labv1.LabSpec{Nodes: map[string]*labv1.NodeExtension{"dsm": {Control: "container"}, "peer": {Control: "container"}}}
	peerSetup := "ip link set eth1 up; ip addr add 192.0.2.10/24 dev eth1; test -z \"$(ip route show default)\""
	if scenario != nil {
		peerSetup, err = scenario(config, spec)
		if err != nil {
			t.Fatal(err)
		}
	}
	spec.Topology, err = clab.Source(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	c, err := client.Launch(ctx, client.Options{LabdPath: daemon, StateDir: filepath.Join(private, "labd-state")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := c.Close(); err != nil {
			t.Error("cleanup", err)
		}
	}()
	lab, err := c.Start(ctx, spec, budget+2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("session=%s socket=%s evidence=%s", lab.ID(), c.Socket(), lab.Artifacts())
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 20*time.Second)
		defer done()
		if r, e := lab.Node("dsm").Exec(cleanup, "tail", "-n", "100", "/dsm-console.log"); e == nil {
			t.Logf("DSM console: %s", r.Stdout)
		}
	}()
	peer := lab.Node("peer")
	r, err := peer.Exec(ctx, "sh", "-ec", peerSetup)
	if err != nil || r.ExitCode != 0 {
		t.Fatalf("peer setup: %v %v", r, err)
	}
	if err := peer.Put(ctx, "/run/dsm-account.env", 0600, credentials); err != nil {
		t.Fatal(err)
	}
	script := `. /run/dsm-account.env
export SSHPASS="$DSM_PASS"
printf '%s\n' "$DSM_PASS" | sshpass -e ssh -o ConnectTimeout=5 -o StrictHostKeyChecking=accept-new "$DSM_USER@192.0.2.20" 'set -e; sudo -S -p "" test -f /root/seaweedfs-lab-account-ready; uname -a; cat /etc.defaults/VERSION; grep -F " /volume1 btrfs " /proc/mounts; echo DSM_AUTHENTICATED_BOOT'
`
	var last string
	for {
		attempt, done := context.WithTimeout(ctx, 15*time.Second)
		r, err := peer.Exec(attempt, "sh", "-ec", script)
		done()
		if err == nil && r.ExitCode == 0 && strings.Contains(string(r.Stdout), "DSM_AUTHENTICATED_BOOT") {
			t.Log(string(r.Stdout))
			if afterBoot != nil {
				afterBoot(ctx, lab)
			}
			// Client.Close removes the VM container, not an orderly guest
			// shutdown. Commit fixture writes before ordinary successful
			// teardown so the next session gets the installed bytes, not an
			// accidental power-loss experiment. Explicit Crash calls inside
			// afterBoot still happen BEFORE this barrier; their durability
			// assertions must pass without any harness-assisted flush.
			dsmRoot(t, ctx, lab, "set -eu\ntest -f /root/seaweedfs-private-image\nsync\necho DSM_FIXTURE_FLUSHED\n")
			return
		}
		if err != nil {
			last = err.Error()
		} else {
			last = string(r.Stderr)
		}
		// A dead wrapper cannot become SSH-ready. Fail immediately rather
		// than hiding an argument/launch failure behind the guest boot wait.
		check, stop := context.WithTimeout(ctx, 10*time.Second)
		_, wrapperErr := lab.Node("dsm").Exec(check, "/bin/true")
		stop()
		if wrapperErr != nil {
			t.Fatalf("DSM VM wrapper unavailable: %v; last SSH result: %s", wrapperErr, last)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("DSM boot did not authenticate: %s", last)
		case <-time.After(5 * time.Second):
		}
	}
}
