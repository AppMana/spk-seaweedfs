package native

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	labv1 "github.com/appmana/labcontainers/api/v1"
	"github.com/appmana/labcontainers/pkg/client"
	matrix "github.com/appmana/labcontainers/pkg/kubernetes"
	"github.com/appmana/labcontainers/pkg/kubernetes/k0s"
	"github.com/appmana/labcontainers/pkg/kubernetes/kube"
	"github.com/distribution/reference"
	native "github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/srl-labs/containerlab/core"
	"github.com/srl-labs/containerlab/links"
	"github.com/srl-labs/containerlab/types"
	v1 "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type dsmClusterInputs struct {
	VMImage, Media, MediaSHA256  string
	DiskScript, DiskScriptSHA256 string
	BackendImage                 string
	SPKVersion, WeedSHA256       string
	Selection                    matrix.Selection
	Images                       *native.ClusterImages
}

func offlineImageIdentity(value string) (tag, canonical, digest string, err error) {
	name, err := reference.ParseNormalizedNamed(value)
	if err != nil {
		return "", "", "", err
	}
	tagged, hasTag := name.(reference.Tagged)
	pinned, hasDigest := name.(reference.Digested)
	if !hasTag || !hasDigest || tagged.Tag() == "latest" {
		return "", "", "", fmt.Errorf("offline image requires source tag and digest")
	}
	base := reference.TrimNamed(name).Name()
	return base + ":" + tagged.Tag(), base + "@" + pinned.Digest().String(), pinned.Digest().String(), nil
}

func TestDSMOfflineImageIdentity(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	tag, canonical, actual, err := offlineImageIdentity("example.test/repo:build-1@" + digest)
	if err != nil || tag != "example.test/repo:build-1" || canonical != "example.test/repo@"+digest || actual != digest {
		t.Fatalf("incorrect offline identity: %s %s %s %v", tag, canonical, actual, err)
	}
	for _, bad := range []string{"example.test/repo:build-1", "example.test/repo@" + digest, "example.test/repo:latest@" + digest} {
		if _, _, _, err := offlineImageIdentity(bad); err == nil {
			t.Fatal("accepted unpinned source:", bad)
		}
	}
}

func TestDSMArtifactPins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "offline-input")
	data := []byte("known lab bytes")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	good := fmt.Sprintf("%x", sha256.Sum256(data))
	if err := verifyDiskFile(path, good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", strings.Repeat("0", 64)} {
		if err := verifyDiskFile(path, bad); err == nil {
			t.Fatal("accepted invalid artifact identity")
		}
	}
	if err := verifyDiskFile("relative-input", good); err == nil {
		t.Fatal("accepted relative input path")
	}
}

func verifyDiskFile(path, expected string) error {
	if !filepath.IsAbs(path) || len(expected) != 64 {
		return fmt.Errorf("explicit absolute artifact path and SHA256 required: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if actual := fmt.Sprintf("%x", h.Sum(nil)); actual != expected {
		return fmt.Errorf("artifact %s SHA256=%s, expected %s", path, actual, expected)
	}
	return nil
}

func clusterScenario(in dsmClusterInputs) dsmScenario {
	return func(config *core.Config, spec *labv1.LabSpec) (string, error) {
		if in.VMImage == "" || !filepath.IsAbs(in.Media) {
			return "", fmt.Errorf("explicit cluster image/media required")
		}
		config.Topology.Nodes["cluster"] = &types.NodeDefinition{
			Kind: "generic_vm", Image: in.VMImage, NetworkMode: "none", ImagePullPolicy: "Never",
			Binds: []string{in.Media + ":/qualification.iso:ro"},
			Env:   map[string]string{"QEMU_MEMORY": "8192", "QEMU_SMP": "4", "QEMU_ADDITIONAL_ARGS": "-drive file=/qualification.iso,format=raw,media=cdrom,readonly=on"},
		}
		config.Topology.Links = append(config.Topology.Links, &links.LinkDefinition{Link: &links.LinkBriefRaw{Endpoints: []string{"peer:eth2", "cluster:eth1"}}})
		spec.Nodes["cluster"] = &labv1.NodeExtension{Control: "qga", Disks: []*labv1.Disk{{Name: "k0s-state", SizeBytes: 32 << 30}}}
		return `ip link add lan type bridge
ip link set eth1 master lan
ip link set eth2 master lan
ip link set eth1 up
ip link set eth2 up
ip link set lan up
ip addr add 192.0.2.10/24 dev lan
test -z "$(ip route show default)"`, nil
	}
}

func TestDSMKubernetesTopologyIsExplicit(t *testing.T) {
	config, err := dsmConfig("/lab/dsm-private.test", "dsm:pinned", "peer:pinned")
	if err != nil {
		t.Fatal(err)
	}
	spec := &labv1.LabSpec{Nodes: map[string]*labv1.NodeExtension{}}
	if _, err := clusterScenario(dsmClusterInputs{VMImage: "linux:pinned", Media: "/lab/media.iso"})(config, spec); err != nil {
		t.Fatal(err)
	}
	if config.Mgmt != nil || spec.AllowExternalAccess || len(config.Topology.Nodes) != 3 || len(config.Topology.Links) != 2 {
		t.Fatal("implicit network or unexpected topology")
	}
	if node := config.Topology.Nodes["cluster"]; node.NetworkMode != "none" || node.ImagePullPolicy != "Never" || len(node.Ports) != 0 {
		t.Fatal("cluster must be isolated and offline")
	}
	if spec.Nodes["cluster"].Control != "qga" {
		t.Fatal("cluster control must use serial QGA")
	}
}

// This is the real Kubernetes backend for DSM join/lifecycle tests, not a mock
// API or a kind cluster. Successful API setup alone is not NAS qualification.
func TestLiveDSMKubernetesJoin(t *testing.T) {
	path := os.Getenv("DSM_KUBERNETES_INPUTS")
	if path == "" {
		t.Skip("requires explicit pinned offline Kubernetes inputs")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var in dsmClusterInputs
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		t.Fatal(err)
	}
	if err := in.Selection.Validate(); err != nil {
		t.Fatal(err)
	}
	if in.Selection.WindowsWorkers || in.Images == nil || in.Images.DefaultPullPolicy != "Never" || !strings.Contains(in.BackendImage, "@sha256:") {
		t.Fatal("isolated pinned Linux backend required")
	}
	if in.SPKVersion == "" || len(in.WeedSHA256) != 64 || strings.Trim(in.WeedSHA256, "0123456789abcdef") != "" {
		t.Fatal("explicit installed package version and weed hash required")
	}
	for path, hash := range map[string]string{in.Media: in.MediaSHA256, in.DiskScript: in.DiskScriptSHA256} {
		if err := verifyDiskFile(path, hash); err != nil {
			t.Fatal(err)
		}
	}
	diskScript, err := os.ReadFile(in.DiskScript)
	if err != nil {
		t.Fatal(err)
	}
	runDSMScenario(t, 25*time.Minute, clusterScenario(in), func(ctx context.Context, lab *client.Session) {
		node := lab.Node("cluster")
		run := func(args ...string) []byte {
			t.Helper()
			out, err := node.Commands().Exec(ctx, args...)
			if err != nil {
				t.Fatalf("cluster %v: %s: %v", args, out, err)
			}
			return out
		}
		wait := func(args ...string) {
			t.Helper()
			limit, cancel := context.WithTimeout(ctx, 6*time.Minute)
			defer cancel()
			for {
				out, err := node.Commands().Exec(limit, args...)
				if err == nil {
					return
				}
				select {
				case <-limit.Done():
					t.Fatalf("cluster readiness %v: %s: %v", args, out, err)
				case <-time.After(3 * time.Second):
				}
			}
		}
		defer func() {
			if !t.Failed() {
				return
			}
			debug, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			out, err := node.Commands().Exec(debug, "sh", "-c", "k0s kubectl get pods,nodes -A -o wide; k0s kubectl get events -A --sort-by=.lastTimestamp | tail -30; journalctl -u k0scontroller --no-pager -n 30")
			t.Logf("Kubernetes diagnostics: %s (%v)", out, err)
		}()
		wait("test", "-b", "/dev/disk/by-label/LCQUAL")
		wait("test", "-b", "/dev/disk/by-id/virtio-lc-k0s-state")
		run("sh", "-ec", "command -v curl; command -v sha256sum")
		t.Log(string(run("sh", "-ec", string(diskScript), "controller-disk", "/var/lib/k0s", "/etc/fstab")))
		run("sh", "-ec", `iface=$(ls /sys/class/net | grep -v '^lo$')
test "$(printf '%s\n' "$iface" | wc -l)" = 1
ip link set "$iface" up
ip addr add 192.0.2.30/24 dev "$iface"
ip route add 10.96.0.0/12 dev "$iface"
ip route add 169.254.1.1/32 dev "$iface"
test -z "$(ip route show default)"
mkdir -p /mnt/qualification /var/lib/k0s/images
mount -o ro /dev/disk/by-label/LCQUAL /mnt/qualification`)
		if hash := strings.Fields(string(run("sha256sum", "/mnt/qualification/k0s"))); len(hash) != 2 || hash[0] != in.Selection.DistributionBinary.SHA256 {
			t.Fatal("guest k0s binary hash mismatch")
		}
		run("sh", "-ec", "install -m 0755 /mnt/qualification/k0s /usr/local/bin/k0s; cp /mnt/qualification/linux-*.tar /var/lib/k0s/images/")
		if version := strings.TrimSpace(string(run("k0s", "version"))); version != in.Selection.DistributionBinary.Version {
			t.Fatalf("k0s version %q", version)
		}
		dnsPatch, err := json.Marshal(&v1.ConfigMap{Data: map[string]string{"Corefile": `.:53 {
    errors
    health
    ready
    kubernetes cluster.local in-addr.arpa ip6.arpa {
        pods insecure
        ttl 30
    }
    prometheus :9153
    cache 30
    reload
    loadbalance
}
`}})
		if err != nil {
			t.Fatal(err)
		}
		config := &native.ClusterConfig{TypeMeta: metav1.TypeMeta{APIVersion: native.ClusterConfigAPIVersion, Kind: native.ClusterConfigKind}, Spec: &native.ClusterSpec{
			API: &native.APISpec{Address: "192.0.2.30", SANs: []string{"192.0.2.30"}}, Images: in.Images,
			Network: &native.Network{PodCIDR: "10.244.0.0/16", ServiceCIDR: "10.96.0.0/12", Calico: &native.Calico{MTU: 1450, IPAutodetectionMethod: "can-reach=192.0.2.20"}},
		}}
		config.Spec.Network.CoreDNS = &native.CoreDNS{Patches: native.Patches{{Target: native.PatchTarget{Kind: "ConfigMap", Name: "coredns", Namespace: "kube-system"}, Patch: native.PatchSpec{Type: native.MergePatchType, Content: string(dnsPatch)}}}}
		if err := k0s.ConfigureNetwork(config, in.Selection); err != nil {
			t.Fatal(err)
		}
		if err := k0s.WriteConfig(ctx, node.Commands(), "/etc/k0s/k0s.yaml", config); err != nil {
			t.Fatal(err)
		}
		if err := k0s.Install(ctx, node.Commands(), "controller", "--enable-worker", "--no-taints", "--config=/etc/k0s/k0s.yaml", "--disable-components=metrics-server,konnectivity-server,autopilot", "--kubelet-extra-args=--node-ip=192.0.2.30 --hostname-override=cluster"); err != nil {
			t.Fatal(err)
		}
		run("k0s", "start")
		wait("k0s", "kubectl", "get", "--raw", "/readyz")
		wait("k0s", "kubectl", "wait", "--for=condition=Ready", "node/cluster", "--timeout=10s")
		wait("k0s", "kubectl", "-n", "kube-system", "rollout", "status", "deployment/coredns", "--timeout=10s")
		// k0s's offline importer registers the archive's tag, not a CRI
		// digest alias. Verify its target before adding the canonical alias;
		// never fetch bytes or relax ImagePullPolicy=Never to make this pass.
		tag, canonical, digest, err := offlineImageIdentity(in.BackendImage)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, line := range strings.Split(string(run("k0s", "ctr", "images", "ls")), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 3 && fields[0] == tag {
				if fields[2] != digest {
					t.Fatalf("offline backend digest %s != %s", fields[2], digest)
				}
				found = true
			}
		}
		if !found {
			t.Fatal("pinned backend source tag was not imported")
		}
		run("k0s", "ctr", "images", "tag", tag, canonical)
		api := &kube.Client{Bastion: node.Commands(), Kubectl: []string{"k0s", "kubectl"}, ControlPlanes: []string{"192.0.2.30"}, APIPort: 6443}
		ns := &v1.Namespace{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"}, ObjectMeta: metav1.ObjectMeta{Name: "seaweedfs-lab"}}
		pod := &v1.Pod{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"}, ObjectMeta: metav1.ObjectMeta{Name: "backend", Namespace: ns.Name, Labels: map[string]string{"app": "seaweed-backend"}}, Spec: v1.PodSpec{
			NodeName: "cluster", HostNetwork: true,
			Containers: []v1.Container{{Name: "weed", Image: canonical, ImagePullPolicy: v1.PullNever, Command: []string{"/usr/bin/weed"}, Args: []string{"server", "-ip=192.0.2.30", "-dir=/data", "-volume=false", "-filer", "-master.volumeSizeLimitMB=64"},
				VolumeMounts:   []v1.VolumeMount{{Name: "data", MountPath: "/data"}},
				ReadinessProbe: &v1.Probe{ProbeHandler: v1.ProbeHandler{HTTPGet: &v1.HTTPGetAction{Path: "/cluster/status", Port: intstr.FromInt32(9333)}}, PeriodSeconds: 2},
			}}, Volumes: []v1.Volume{{Name: "data", VolumeSource: v1.VolumeSource{EmptyDir: &v1.EmptyDirVolumeSource{}}}},
		}}
		svc := &v1.Service{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Service"}, ObjectMeta: metav1.ObjectMeta{Name: "seaweed-master", Namespace: ns.Name}, Spec: v1.ServiceSpec{Selector: pod.Labels, Ports: []v1.ServicePort{{Name: "master-http", Port: 9333, TargetPort: intstr.FromInt32(9333)}}}}
		if err := api.ApplyObjects(ctx, ns, pod, svc); err != nil {
			t.Fatal(err)
		}
		wait("k0s", "kubectl", "wait", "--namespace="+ns.Name, "--for=condition=Ready", "pod/backend", "--timeout=10s")
		wait("sh", "-ec", "test \"$(k0s kubectl -n seaweedfs-lab get endpoints seaweed-master -o jsonpath='{.subsets[0].addresses[0].ip}')\" = 192.0.2.30")
		t.Log("DSM_KUBERNETES_BACKEND_READY: real pod, Service and controller-generated Endpoints")
		sa := &v1.ServiceAccount{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ServiceAccount"}, ObjectMeta: metav1.ObjectMeta{Name: "dsm", Namespace: ns.Name}}
		role := &rbac.Role{TypeMeta: metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "Role"}, ObjectMeta: metav1.ObjectMeta{Name: "dsm", Namespace: ns.Name}, Rules: []rbac.PolicyRule{{APIGroups: []string{""}, Resources: []string{"services", "endpoints"}, Verbs: []string{"get"}}}}
		binding := &rbac.RoleBinding{TypeMeta: metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "RoleBinding"}, ObjectMeta: metav1.ObjectMeta{Name: "dsm", Namespace: ns.Name}, RoleRef: rbac.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "dsm"}, Subjects: []rbac.Subject{{Kind: "ServiceAccount", Name: "dsm", Namespace: ns.Name}}}
		if err := api.ApplyObjects(ctx, sa, role, binding); err != nil {
			t.Fatal(err)
		}
		token, err := api.Run(ctx, "-n", ns.Name, "create", "token", "dsm", "--duration=1h")
		if err != nil {
			t.Fatal("create lab-only service-account token:", err)
		}
		tokenPath := dsmUpload(t, ctx, lab, "kube-token", token)
		caPath := dsmUpload(t, ctx, lab, "kube-ca.crt", run("cat", "/var/lib/k0s/pki/ca.crt"))
		dataDir := "/volume1/seaweedfs-kubernetes-" + lab.ID()
		volumeConfig, err := json.Marshal(map[string]any{
			"kube":   map[string]any{"apiserver": "https://192.0.2.30:6443", "tokenFile": "/var/packages/seaweedfs/var/kube/token", "caFile": "/var/packages/seaweedfs/var/kube/ca.crt", "namespace": ns.Name, "masterService": svc.Name, "masterAddressMode": "endpoints"},
			"volume": map[string]any{"dir": dataDir, "ip": "192.0.2.20", "port": 8080, "grpcPort": 18080, "dataCenter": "lab", "rack": "synology", "max": 32, "instances": 1},
		})
		if err != nil {
			t.Fatal(err)
		}
		configPath := dsmUpload(t, ctx, lab, "volume.json", volumeConfig)
		dsmRoot(t, ctx, lab, `set -eu
test -f /root/seaweedfs-private-image
trap 'result=$?; trap - EXIT; if [ "$result" != 0 ]; then
  systemctl status pkgctl-seaweedfs.service pkg-seaweedfs-volume.service --no-pager || true
  journalctl -u pkg-seaweedfs-volume.service --no-pager -n 60 || true
  tail -30 /var/log/packages/seaweedfs.log || true
fi; exit "$result"' EXIT
pkg=/var/packages/seaweedfs
grep -Fx `+shellQuote(`version="`+in.SPKVersion+`"`)+` "$pkg/INFO"
printf '%s  %s\n' `+shellQuote(in.WeedSHA256)+` "$pkg/target/bin/weed" | sha256sum -c -
/usr/syno/bin/synopkg stop seaweedfs
cp -p "$pkg/var/volume.yaml" "$pkg/var/volume.yaml.before-kubernetes-`+lab.ID()+`"
install -d -m 755 -o sc-seaweedfs `+shellQuote(dataDir)+`
install -m 600 -o sc-seaweedfs `+shellQuote(tokenPath)+` "$pkg/var/kube/token"
install -m 644 -o sc-seaweedfs `+shellQuote(caPath)+` "$pkg/var/kube/ca.crt"
install -m 600 -o sc-seaweedfs `+shellQuote(configPath)+` "$pkg/var/volume.yaml"
# Persist fixture provisioning BEFORE starting the daemon or writing the
# workload. Otherwise power loss can restore the seed's empty config instead
# of exercising acknowledged-data recovery. Never move this after data writes.
sync
/usr/syno/bin/synopkg start seaweedfs
sleep 5
/usr/syno/bin/synopkg status seaweedfs
tail -40 "$pkg/var/log/weed.log"
`)
		wait("sh", "-ec", "curl -fsS http://192.0.2.30:9333/dir/status | grep -F '192.0.2.20:8080'")
		dsmRoot(t, ctx, lab, `set -eu
uid=$(id -u sc-seaweedfs)
found=0
for pid in $(pidof weed); do
  test "$(stat -c %u /proc/$pid)" = "$uid" || continue
  found=$((found+1))
  grep -E '^Max open files[[:space:]]+65536[[:space:]]+65536' /proc/$pid/limits
  grep -E '^Max processes[[:space:]]+4096[[:space:]]+4096' /proc/$pid/limits
  cg=$(awk -F: '$2 == "memory" {print $3}' /proc/$pid/cgroup)
  test "$cg" = /seaweedfs.slice/pkg-seaweedfs-volume.service
  test "$(cat /sys/fs/cgroup/memory$cg/memory.limit_in_bytes)" = 5368709120
  if tr '\000' '\n' < /proc/$pid/environ | grep -q '^GOMEMLIMIT='; then
    echo 'single-instance DSM process must derive memory from its cgroup' >&2
    exit 1
  fi
  if tr '\000' '\n' < /proc/$pid/cmdline | grep -Eq '^-concurrent(Upload|Download)LimitMB'; then
    echo 'DSM process must use automatic admission defaults' >&2
    exit 1
  fi
  printf 'DSM_RUNNING_RESOURCE_LIMITS_PASS pid=%s uid=%s cgroup=%s\n' "$pid" "$uid" "$cg"
done
test "$found" = 1
grep -E 'memory limits: available [1-9][0-9]* .*GOMEMLIMIT env false, Go memory limit [1-9][0-9]* \(set true\), upload admission [1-9][0-9]* MiB \(auto true\), download admission [1-9][0-9]* MiB \(auto true\)' /var/packages/seaweedfs/var/log/weed.log
`)
		t.Log("DSM_KUBERNETES_JOIN_PASS: authenticated discovery, real topology membership and running-process resource limits")
		verifyDSMDataRecovery(t, ctx, lab)
	})
}
