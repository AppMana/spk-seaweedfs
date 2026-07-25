// synology-volume-bootstrap renders `weed volume` argv from a single
// volume.yaml file plus live discovery against the Kubernetes API.
//
// It runs at every package start (and on demand via the DSM "Configure"
// panel). Output is written to $SYNOPKG_PKGVAR/run/argv as one CLI flag
// per line; service_prestart in the SPK reads that file into a bash
// array and execs `weed volume`.
//
// The kube apiserver token authenticates only this discovery client;
// SeaweedFS volume<->master heartbeats use no auth (membership = LAN
// reachability + the dataCenter/rack/ip/publicUrl this volume self-
// reports). Optional mTLS material lives in a Secret referenced by
// volume.yaml's mtls.secretName.
package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type config struct {
	Kube struct {
		APIServer   string `yaml:"apiserver"`
		TokenFile   string `yaml:"tokenFile"`
		CAFile      string `yaml:"caFile"`
		Insecure    bool   `yaml:"insecureSkipTLSVerify"`
		Namespace   string `yaml:"namespace"`
		SeaweedName string `yaml:"seaweedName"`
		Group       string `yaml:"group"`
		// MasterService, when set, discovers masters directly from this
		// Service's Endpoints — no Seaweed CRD/operator required. Use
		// this against a plain Helm-chart deployment (no operator
		// installed), which is how AppMana's own production SeaweedFS
		// runs. Mutually exclusive with SeaweedName in practice, but
		// MasterService wins if both are set.
		MasterService string `yaml:"masterService"`
		// MasterPort is the master's HTTP port used with MasterService
		// (the CRD path instead reads this from the Seaweed CR).
		// Defaults to 9333.
		MasterPort int `yaml:"masterPort"`
	} `yaml:"kube"`
	Volume struct {
		Dir        string `yaml:"dir"`
		IP         string `yaml:"ip"`
		PublicURL  string `yaml:"publicUrl"`
		Port       int    `yaml:"port"`
		GRPCPort   int    `yaml:"grpcPort"`
		DataCenter string `yaml:"dataCenter"`
		Rack       string `yaml:"rack"`
		Max        int    `yaml:"max"`
		DiskType   string `yaml:"diskType"`
		Index      string `yaml:"index"`
		ExtraFlags []string `yaml:"extraFlags"`
		// Instances runs N `weed volume` processes (default 1), all in
		// the same rack. SeaweedFS's volume growth requires every rack
		// participating in a replication policy with a same-rack copy
		// count (e.g. production's "011") to have >= sameRackCount+1
		// data nodes — a single-process NAS can never be selected under
		// such policies (weed/topology/volume_growth.go). Instance i
		// serves port+i / grpcPort+i from <dir>/v<i>.
		Instances int `yaml:"instances"`
	} `yaml:"volume"`
	MTLS struct {
		SecretName string `yaml:"secretName"`
	} `yaml:"mtls"`
	Weed struct {
		Image     string   `yaml:"image"`
		Digest    string   `yaml:"digest"`
		PlainHTTP bool     `yaml:"plainHTTP"`
		Binaries  []string `yaml:"binaries"`
		CacheDir  string   `yaml:"cacheDir"`
	} `yaml:"weed"`
}

// defaultSeaweedGroup is the API group of the upstream
// seaweedfs-operator's Seaweed CRD. Overridable via kube.group for
// operator forks that rename it.
const defaultSeaweedGroup = "seaweed.seaweedfs.com"

func seaweedGVR(c *config) schema.GroupVersionResource {
	group := c.Kube.Group
	if group == "" {
		group = defaultSeaweedGroup
	}
	return schema.GroupVersionResource{
		Group:    group,
		Version:  "v1",
		Resource: "seaweeds",
	}
}

func main() {
	var (
		configPath = flag.String("config", os.Getenv("BOOTSTRAP_CONFIG"), "path to volume.yaml")
		outPath    = flag.String("out", os.Getenv("BOOTSTRAP_OUT"), "argv output file (one flag per line)")
		tlsDir     = flag.String("tls-dir", os.Getenv("BOOTSTRAP_TLS_DIR"), "directory to write mTLS material into")
		weedBinOut = flag.String("weed-bin-out", os.Getenv("BOOTSTRAP_WEED_BIN_OUT"), "file to write the resolved weed binary path into (empty when weed.image is unset)")
		printOnly  = flag.Bool("print-only", false, "print argv to stdout, do not write any file")
		timeout    = flag.Duration("timeout", 30*time.Second, "kube discovery timeout")
	)
	flag.Parse()

	if *configPath == "" {
		fatal("missing --config (or $BOOTSTRAP_CONFIG)")
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		fatal("load config: %v", err)
	}
	if err := validate(cfg); err != nil {
		fatal("invalid config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	rc, err := buildRESTConfig(cfg)
	if err != nil {
		fatal("build rest config: %v", err)
	}
	core, err := kubernetes.NewForConfig(rc)
	if err != nil {
		fatal("kubernetes client: %v", err)
	}
	dyn, err := dynamic.NewForConfig(rc)
	if err != nil {
		fatal("dynamic client: %v", err)
	}

	weedBin, err := materializeWeed(ctx, cfg)
	if err != nil {
		fatal("materialize weed from OCI image: %v", err)
	}

	masters, err := discoverMasters(ctx, dyn, core, cfg)
	if err != nil {
		fatal("discover masters: %v", err)
	}

	tlsArgs, err := materializeMTLS(ctx, core, cfg, *tlsDir)
	if err != nil {
		fatal("mtls material: %v", err)
	}

	if *printOnly {
		if weedBin != "" {
			fmt.Fprintf(os.Stderr, "weed binary: %s\n", weedBin)
		}
		for i := 0; i < cfg.Volume.Instances; i++ {
			if cfg.Volume.Instances > 1 {
				fmt.Printf("# instance %d\n", i)
			}
			for _, a := range renderArgs(instanceConfig(cfg, i), masters, tlsArgs) {
				fmt.Println(a)
			}
		}
		return
	}
	if *outPath == "" {
		fatal("missing --out (or $BOOTSTRAP_OUT)")
	}
	// weed_bin is written before any argv file: run.sh instances > 0
	// wait for their argv file to appear and then read weed_bin, so
	// argv appearing must imply weed_bin is already in place.
	if *weedBinOut != "" {
		// Written even when empty so a weed.image removal reverts the
		// service to the packaged binary on the next restart.
		if err := writeArgv(*weedBinOut, []string{weedBin}); err != nil {
			fatal("write weed-bin-out: %v", err)
		}
	} else if weedBin != "" {
		fatal("weed.image set but --weed-bin-out (or $BOOTSTRAP_WEED_BIN_OUT) missing")
	}
	for i := 0; i < cfg.Volume.Instances; i++ {
		icfg := instanceConfig(cfg, i)
		if err := os.MkdirAll(icfg.Volume.Dir, 0o755); err != nil {
			fatal("create volume dir for instance %d: %v", i, err)
		}
		path := *outPath
		if i > 0 {
			path = fmt.Sprintf("%s.%d", *outPath, i)
		}
		if err := writeArgv(path, renderArgs(icfg, masters, tlsArgs)); err != nil {
			fatal("write argv for instance %d: %v", i, err)
		}
	}
	fmt.Fprintf(os.Stderr, "wrote argv for %d instance(s) to %s\n", cfg.Volume.Instances, *outPath)
}

// instanceConfig returns a copy of c adjusted for volume-server
// instance i: with instances > 1, instance i serves port+i / grpcPort+i
// from <dir>/v<i>, and publicUrl is re-derived from ip and the
// instance's port (an explicitly configured publicUrl would otherwise
// wrongly point every instance at instance 0's port). A single-instance
// config is returned unchanged, preserving the existing on-disk layout
// of deployments made before instances existed.
func instanceConfig(c *config, i int) *config {
	if c.Volume.Instances <= 1 {
		return c
	}
	ic := *c
	ic.Volume.Dir = filepath.Join(c.Volume.Dir, fmt.Sprintf("v%d", i))
	ic.Volume.Port = c.Volume.Port + i
	ic.Volume.GRPCPort = c.Volume.GRPCPort + i
	ic.Volume.PublicURL = fmt.Sprintf("%s:%d", c.Volume.IP, ic.Volume.Port)
	return &ic
}

func loadConfig(path string) (*config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func validate(c *config) error {
	if c.Kube.APIServer == "" {
		return errors.New("kube.apiserver is required")
	}
	if _, err := url.Parse(c.Kube.APIServer); err != nil {
		return fmt.Errorf("kube.apiserver: %w", err)
	}
	if c.Kube.TokenFile == "" {
		return errors.New("kube.tokenFile is required")
	}
	if c.Kube.Namespace == "" {
		return errors.New("kube.namespace is required")
	}
	if c.Kube.SeaweedName == "" && c.Kube.MasterService == "" {
		return errors.New("kube.seaweedName or kube.masterService is required")
	}
	if c.Kube.MasterService != "" && c.Kube.MasterPort == 0 {
		c.Kube.MasterPort = 9333
	}
	if c.Volume.Dir == "" {
		return errors.New("volume.dir is required")
	}
	if c.Volume.IP == "" {
		return errors.New("volume.ip is required")
	}
	if c.Volume.Port == 0 {
		c.Volume.Port = 8080
	}
	if c.Volume.GRPCPort == 0 {
		c.Volume.GRPCPort = c.Volume.Port + 10000
	}
	if c.Volume.PublicURL == "" {
		c.Volume.PublicURL = fmt.Sprintf("%s:%d", c.Volume.IP, c.Volume.Port)
	}
	if c.Volume.DataCenter == "" {
		c.Volume.DataCenter = "synology"
	}
	if c.Volume.Rack == "" {
		hn, _ := os.Hostname()
		if hn == "" {
			hn = "nas"
		}
		c.Volume.Rack = hn
	}
	if c.Volume.Max == 0 {
		c.Volume.Max = 1000
	}
	if c.Volume.Instances == 0 {
		c.Volume.Instances = 1
	}
	if c.Volume.Instances < 1 || c.Volume.Instances > 8 {
		return fmt.Errorf("volume.instances must be 1..8, got %d", c.Volume.Instances)
	}
	return nil
}

func buildRESTConfig(c *config) (*rest.Config, error) {
	tokenBytes, err := os.ReadFile(c.Kube.TokenFile)
	if err != nil {
		return nil, fmt.Errorf("read tokenFile: %w", err)
	}
	rc := &rest.Config{
		Host:        c.Kube.APIServer,
		BearerToken: strings.TrimSpace(string(tokenBytes)),
		Timeout:     20 * time.Second,
	}
	if c.Kube.Insecure {
		rc.TLSClientConfig = rest.TLSClientConfig{Insecure: true}
	} else if c.Kube.CAFile != "" {
		rc.TLSClientConfig = rest.TLSClientConfig{CAFile: c.Kube.CAFile}
	}
	return rc, nil
}

// discoverMasters returns the comma-separated value passed to `weed
// volume -mserver=`. That flag takes each master's HTTP port, not its
// gRPC port: weed derives the gRPC port itself by adding 10000
// (pb.ServerToGrpcAddress). Passing the gRPC port here makes the
// volume server dial http_port+20000 and fail to connect.
//
// Two discovery modes:
//   - kube.masterService set: read that Service's Endpoints directly.
//     No Seaweed CRD or operator required — this is what a plain
//     Helm-chart SeaweedFS deployment (no operator installed) needs,
//     which is how AppMana's own production cluster runs it.
//   - kube.seaweedName set (masterService empty): the original
//     operator-CRD path — reads the Seaweed CR for the master's HTTP
//     port and replica count, preferring the master Service object
//     (service-name format may drift between operator releases) and
//     falling back to the CR's spec.master.replicas via the headless
//     Service if the master Service does not yet exist.
func discoverMasters(ctx context.Context, dyn dynamic.Interface, core kubernetes.Interface, c *config) (string, error) {
	if c.Kube.MasterService != "" {
		endpoints, err := masterEndpointsFromServiceName(ctx, core, c, c.Kube.MasterService, c.Kube.MasterPort)
		if err != nil {
			return "", fmt.Errorf("get master service %s/%s: %w", c.Kube.Namespace, c.Kube.MasterService, err)
		}
		if endpoints == "" {
			return "", fmt.Errorf("master service %s/%s has no ready endpoints", c.Kube.Namespace, c.Kube.MasterService)
		}
		return endpoints, nil
	}

	cr, err := dyn.Resource(seaweedGVR(c)).Namespace(c.Kube.Namespace).Get(ctx, c.Kube.SeaweedName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get Seaweed %s/%s: %w", c.Kube.Namespace, c.Kube.SeaweedName, err)
	}

	masterPort := masterHTTPPort(cr)
	if endpoints, err := masterEndpointsFromService(ctx, core, c, masterPort); err == nil && endpoints != "" {
		return endpoints, nil
	}

	replicas := int(masterReplicas(cr))
	if replicas == 0 {
		return "", fmt.Errorf("Seaweed %s/%s: spec.master.replicas missing or zero", c.Kube.Namespace, c.Kube.SeaweedName)
	}
	headless := fmt.Sprintf("%s-master.%s.svc", c.Kube.SeaweedName, c.Kube.Namespace)
	parts := make([]string, 0, replicas)
	for i := 0; i < replicas; i++ {
		parts = append(parts, fmt.Sprintf("%s-master-%d.%s:%d", c.Kube.SeaweedName, i, headless, masterPort))
	}
	return strings.Join(parts, ","), nil
}

func masterEndpointsFromService(ctx context.Context, core kubernetes.Interface, c *config, port int) (string, error) {
	candidates := []string{
		c.Kube.SeaweedName + "-master",
		c.Kube.SeaweedName + "-master-headless",
		c.Kube.SeaweedName + "-masters",
	}
	for _, name := range candidates {
		addrs, err := masterEndpointsFromServiceName(ctx, core, c, name, port)
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return "", err
		}
		if addrs != "" {
			return addrs, nil
		}
	}
	return "", nil
}

// masterEndpointsFromServiceName resolves one specific Service's
// Endpoints into a "host:port,host:port" list using that Service's
// HTTP port (matched by name "master-http"/"http", falling back to the
// given default port). Returns "" (no error) if the Service exists but
// has no ready endpoints yet.
//
// Always uses the Endpoints' pod IPs, never a cluster-internal *.svc
// DNS name — even though a headless Service's Endpoints often carry a
// per-pod Hostname (e.g. StatefulSet pods with a matching subdomain),
// that name only resolves via in-cluster coredns. This bootstrap's
// caller is always an external LAN client (the Synology), for which
// only the routable pod IP works. (Confirmed against the real
// production cluster 2026-07-21: pod IPs are BGP-routed to the LAN
// with no extra plumbing, but *.svc names are not resolvable at all
// from outside the cluster — "no such host".)
func masterEndpointsFromServiceName(ctx context.Context, core kubernetes.Interface, c *config, name string, port int) (string, error) {
	svc, err := core.CoreV1().Services(c.Kube.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	ep, err := core.CoreV1().Endpoints(c.Kube.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	httpPort := int32(port)
	for _, p := range svc.Spec.Ports {
		if p.Name == "master-http" || p.Name == "http" || p.Port == int32(port) {
			httpPort = p.Port
			break
		}
	}
	var addrs []string
	for _, ss := range ep.Subsets {
		for _, a := range ss.Addresses {
			addrs = append(addrs, fmt.Sprintf("%s:%d", a.IP, httpPort))
		}
	}
	if len(addrs) == 0 {
		return "", nil
	}
	sort.Strings(addrs)
	return strings.Join(addrs, ","), nil
}

func masterReplicas(cr *unstructured.Unstructured) int64 {
	if v, found, _ := unstructured.NestedInt64(cr.Object, "spec", "master", "replicas"); found {
		return v
	}
	return 0
}

// SeaweedFS master HTTP port — what `-mserver=` expects. The operator
// exposes it via the master's Service; defaults to 9333.
func masterHTTPPort(cr *unstructured.Unstructured) int {
	if p, found, _ := unstructured.NestedInt64(cr.Object, "spec", "master", "port"); found {
		return int(p)
	}
	return 9333
}

// materializeMTLS pulls tls.crt / tls.key / ca.crt from a Secret in the
// configured namespace, validates the PEM, writes the files to tlsDir
// (mode 0600), and returns the corresponding -volume.* flags. No-op
// when mtls.secretName is empty.
func materializeMTLS(ctx context.Context, core kubernetes.Interface, c *config, tlsDir string) ([]string, error) {
	if c.MTLS.SecretName == "" {
		return nil, nil
	}
	if tlsDir == "" {
		return nil, errors.New("mtls.secretName set but --tls-dir/$BOOTSTRAP_TLS_DIR is empty")
	}
	secret, err := core.CoreV1().Secrets(c.Kube.Namespace).Get(ctx, c.MTLS.SecretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get mtls secret %s/%s: %w", c.Kube.Namespace, c.MTLS.SecretName, err)
	}
	if err := os.MkdirAll(tlsDir, 0o700); err != nil {
		return nil, err
	}
	files := map[string]string{
		"tls.crt": "cert.pem",
		"tls.key": "key.pem",
		"ca.crt":  "ca.pem",
	}
	written := map[string]string{}
	for k, fn := range files {
		data, ok := secret.Data[k]
		if !ok || len(data) == 0 {
			return nil, fmt.Errorf("mtls secret %s missing key %q", c.MTLS.SecretName, k)
		}
		if k != "tls.key" {
			block, _ := pem.Decode(data)
			if block == nil {
				return nil, fmt.Errorf("mtls secret %s key %q: not PEM", c.MTLS.SecretName, k)
			}
			if _, err := x509.ParseCertificate(block.Bytes); err != nil {
				return nil, fmt.Errorf("mtls secret %s key %q: %w", c.MTLS.SecretName, k, err)
			}
		}
		dst := filepath.Join(tlsDir, fn)
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			return nil, err
		}
		written[k] = dst
	}
	return []string{
		fmt.Sprintf("-volume.cert.file=%s", written["tls.crt"]),
		fmt.Sprintf("-volume.key.file=%s", written["tls.key"]),
		fmt.Sprintf("-volume.ca.file=%s", written["ca.crt"]),
	}, nil
}

func renderArgs(c *config, masters string, tlsArgs []string) []string {
	args := []string{
		"-mserver=" + masters,
		"-ip=" + c.Volume.IP,
		"-publicUrl=" + c.Volume.PublicURL,
		fmt.Sprintf("-port=%d", c.Volume.Port),
		fmt.Sprintf("-port.grpc=%d", c.Volume.GRPCPort),
		"-dataCenter=" + c.Volume.DataCenter,
		"-rack=" + c.Volume.Rack,
		"-dir=" + c.Volume.Dir,
		fmt.Sprintf("-max=%d", c.Volume.Max),
	}
	if c.Volume.DiskType != "" {
		args = append(args, "-disk="+c.Volume.DiskType)
	}
	if c.Volume.Index != "" {
		args = append(args, "-index="+c.Volume.Index)
	}
	args = append(args, tlsArgs...)
	args = append(args, c.Volume.ExtraFlags...)
	return args
}

func writeArgv(path string, args []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	body := strings.Join(args, "\n") + "\n"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "synology-volume-bootstrap: "+format+"\n", args...)
	os.Exit(1)
}
