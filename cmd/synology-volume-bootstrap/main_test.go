package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	corefake "k8s.io/client-go/kubernetes/fake"
)

func makeSeaweedCR(name, ns string, replicas int64) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "seaweed.seaweedfs.com",
		Version: "v1",
		Kind:    "Seaweed",
	})
	u.SetName(name)
	u.SetNamespace(ns)
	u.Object["spec"] = map[string]any{
		"master": map[string]any{
			"replicas": replicas,
		},
	}
	return u
}

func TestRenderArgs_BasicShape(t *testing.T) {
	c := &config{}
	c.Volume.IP = "10.2.0.73"
	c.Volume.Port = 8080
	c.Volume.GRPCPort = 18080
	c.Volume.PublicURL = "10.2.0.73:8080"
	c.Volume.Dir = "/volume1/seaweedfs"
	c.Volume.DataCenter = "synology"
	c.Volume.Rack = "appmana-017-ds"
	c.Volume.Max = 1000
	c.Volume.DiskType = "hdd"
	c.Volume.Index = "leveldbMedium"
	c.Volume.ExtraFlags = []string{"-readMode=local"}

	args := renderArgs(c, "10.96.7.10:19333,10.96.7.11:19333", []string{"-volume.cert.file=/x/cert.pem"})

	got := strings.Join(args, " ")
	want := []string{
		"-mserver=10.96.7.10:19333,10.96.7.11:19333",
		"-ip=10.2.0.73",
		"-publicUrl=10.2.0.73:8080",
		"-port=8080",
		"-port.grpc=18080",
		"-dataCenter=synology",
		"-rack=appmana-017-ds",
		"-dir=/volume1/seaweedfs",
		"-max=1000",
		"-disk=hdd",
		"-index=leveldbMedium",
		"-volume.cert.file=/x/cert.pem",
		"-readMode=local",
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("renderArgs missing %q\nfull: %s", w, got)
		}
	}
}

func TestValidate_Defaults(t *testing.T) {
	c := &config{}
	c.Kube.APIServer = "https://api.example:6443"
	c.Kube.TokenFile = "/etc/token"
	c.Kube.Namespace = "seaweedfs"
	c.Kube.SeaweedName = "appmana"
	c.Volume.Dir = "/d"
	c.Volume.IP = "10.0.0.1"

	if err := validate(c); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if c.Volume.Port != 8080 {
		t.Errorf("Port default = %d, want 8080", c.Volume.Port)
	}
	if c.Volume.GRPCPort != 18080 {
		t.Errorf("GRPCPort default = %d, want 18080", c.Volume.GRPCPort)
	}
	if c.Volume.PublicURL != "10.0.0.1:8080" {
		t.Errorf("PublicURL default = %q, want 10.0.0.1:8080", c.Volume.PublicURL)
	}
	if c.Volume.DataCenter != "synology" {
		t.Errorf("DataCenter default = %q, want synology", c.Volume.DataCenter)
	}
	if c.Volume.Max != 1000 {
		t.Errorf("Max default = %d, want 1000", c.Volume.Max)
	}
}

func TestValidate_Required(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(c *config)
		want  string
	}{
		{"apiserver", func(c *config) { c.Kube.APIServer = "" }, "kube.apiserver"},
		{"tokenFile", func(c *config) { c.Kube.TokenFile = "" }, "kube.tokenFile"},
		{"namespace", func(c *config) { c.Kube.Namespace = "" }, "kube.namespace"},
		{"seaweedName", func(c *config) { c.Kube.SeaweedName = "" }, "kube.seaweedName"},
		{"dir", func(c *config) { c.Volume.Dir = "" }, "volume.dir"},
		{"ip", func(c *config) { c.Volume.IP = "" }, "volume.ip"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &config{}
			c.Kube.APIServer = "https://api:6443"
			c.Kube.TokenFile = "/t"
			c.Kube.Namespace = "ns"
			c.Kube.SeaweedName = "n"
			c.Volume.Dir = "/d"
			c.Volume.IP = "10.0.0.1"
			tc.mut(c)
			err := validate(c)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("validate: got %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestValidate_MasterServiceSatisfiesRequirement(t *testing.T) {
	// kube.masterService alone (no kube.seaweedName) must validate —
	// this is the CRD-free path for clusters with no seaweedfs-operator.
	c := &config{}
	c.Kube.APIServer = "https://api.example:6443"
	c.Kube.TokenFile = "/etc/token"
	c.Kube.Namespace = "seaweedfs"
	c.Kube.MasterService = "seaweedfs-master"
	c.Volume.Dir = "/d"
	c.Volume.IP = "10.0.0.1"

	if err := validate(c); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if c.Kube.MasterPort != 9333 {
		t.Errorf("MasterPort default = %d, want 9333", c.Kube.MasterPort)
	}
}

func TestValidate_NeitherSeaweedNameNorMasterService(t *testing.T) {
	c := &config{}
	c.Kube.APIServer = "https://api.example:6443"
	c.Kube.TokenFile = "/etc/token"
	c.Kube.Namespace = "seaweedfs"
	c.Volume.Dir = "/d"
	c.Volume.IP = "10.0.0.1"

	err := validate(c)
	if err == nil || !strings.Contains(err.Error(), "kube.masterService") {
		t.Fatalf("expected error mentioning kube.masterService, got %v", err)
	}
}

func TestDiscoverMasters_FromMasterServiceNoCRD(t *testing.T) {
	// No Seaweed CR exists anywhere in this fake client — proves the
	// masterService path never touches the dynamic/CRD client, which is
	// the point: production runs a plain Helm-chart SeaweedFS with no
	// seaweedfs-operator installed at all.
	scheme := runtime.NewScheme()
	dyn := dynfake.NewSimpleDynamicClient(scheme) // deliberately empty

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "seaweedfs-master", Namespace: "seaweedfs"},
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.152.184.50",
			Ports: []corev1.ServicePort{
				{Name: "master-http", Port: 9333},
				{Name: "master-grpc", Port: 19333},
			},
		},
	}
	ep := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Name: "seaweedfs-master", Namespace: "seaweedfs"},
		Subsets: []corev1.EndpointSubset{
			{Addresses: []corev1.EndpointAddress{
				{IP: "10.3.0.7"},
				{IP: "10.3.0.8"},
				{IP: "10.3.0.9"},
			}},
		},
	}
	core := corefake.NewSimpleClientset(svc, ep)

	c := &config{}
	c.Kube.Namespace = "seaweedfs"
	c.Kube.MasterService = "seaweedfs-master"
	c.Kube.MasterPort = 9333

	got, err := discoverMasters(context.Background(), dyn, core, c)
	if err != nil {
		t.Fatalf("discoverMasters: %v", err)
	}
	for _, want := range []string{"10.3.0.7:9333", "10.3.0.8:9333", "10.3.0.9:9333"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in result, got: %s", want, got)
		}
	}
	if strings.Contains(got, "19333") {
		t.Errorf("must use the HTTP port, not gRPC: %s", got)
	}
}

func TestDiscoverMasters_MasterServiceMissingEndpoints(t *testing.T) {
	scheme := runtime.NewScheme()
	dyn := dynfake.NewSimpleDynamicClient(scheme)
	core := corefake.NewSimpleClientset() // no Service at all

	c := &config{}
	c.Kube.Namespace = "seaweedfs"
	c.Kube.MasterService = "seaweedfs-master"
	c.Kube.MasterPort = 9333

	_, err := discoverMasters(context.Background(), dyn, core, c)
	if err == nil {
		t.Fatal("expected an error when the master service does not exist")
	}
}

func TestDiscoverMasters_FromHeadlessFallback(t *testing.T) {
	cr := makeSeaweedCR("appmana", "seaweedfs", 3)
	scheme := runtime.NewScheme()
	dyn := dynfake.NewSimpleDynamicClient(scheme, cr)
	core := corefake.NewSimpleClientset() // no Service objects → falls back to constructed DNS

	c := &config{}
	c.Kube.Namespace = "seaweedfs"
	c.Kube.SeaweedName = "appmana"

	got, err := discoverMasters(context.Background(), dyn, core, c)
	if err != nil {
		t.Fatalf("discoverMasters: %v", err)
	}
	// :9333 (HTTP port) — weed volume's -mserver derives the gRPC port
	// itself by adding 10000; passing 19333 here would make it dial 29333.
	want := "appmana-master-0.appmana-master.seaweedfs.svc:9333,appmana-master-1.appmana-master.seaweedfs.svc:9333,appmana-master-2.appmana-master.seaweedfs.svc:9333"
	if got != want {
		t.Errorf("masters mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestDiscoverMasters_FromService(t *testing.T) {
	cr := makeSeaweedCR("appmana", "seaweedfs", 3)
	scheme := runtime.NewScheme()
	dyn := dynfake.NewSimpleDynamicClient(scheme, cr)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "appmana-master", Namespace: "seaweedfs"},
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.96.7.10",
			Ports: []corev1.ServicePort{
				{Name: "master-http", Port: 9333},
				{Name: "master-grpc", Port: 19333},
			},
		},
	}
	ep := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Name: "appmana-master", Namespace: "seaweedfs"},
		Subsets: []corev1.EndpointSubset{
			{Addresses: []corev1.EndpointAddress{
				{IP: "10.96.7.10"},
				{IP: "10.96.7.11"},
				{IP: "10.96.7.12"},
			}},
		},
	}
	core := corefake.NewSimpleClientset(svc, ep)

	c := &config{}
	c.Kube.Namespace = "seaweedfs"
	c.Kube.SeaweedName = "appmana"

	got, err := discoverMasters(context.Background(), dyn, core, c)
	if err != nil {
		t.Fatalf("discoverMasters: %v", err)
	}
	if !strings.Contains(got, "10.96.7.10:9333") || !strings.Contains(got, "10.96.7.12:9333") {
		t.Errorf("expected pod IPs with the HTTP port, got: %s", got)
	}
	if strings.Contains(got, "19333") {
		t.Errorf("mserver must carry the HTTP port, not the gRPC port: %s", got)
	}
}

func TestDiscoverMasters_PortFallback(t *testing.T) {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "seaweed.seaweedfs.com", Version: "v1", Kind: "Seaweed",
	})
	u.SetName("appmana")
	u.SetNamespace("seaweedfs")
	u.Object["spec"] = map[string]any{
		"master": map[string]any{
			"replicas": int64(1),
			"port":     int64(9443),
		},
	}
	scheme := runtime.NewScheme()
	dyn := dynfake.NewSimpleDynamicClient(scheme, u)
	core := corefake.NewSimpleClientset()

	c := &config{}
	c.Kube.Namespace = "seaweedfs"
	c.Kube.SeaweedName = "appmana"

	got, err := discoverMasters(context.Background(), dyn, core, c)
	if err != nil {
		t.Fatalf("discoverMasters: %v", err)
	}
	if !strings.HasSuffix(got, ":9443") {
		t.Errorf("expected the CR's custom HTTP port 9443, got %s", got)
	}
}

func TestMaterializeMTLS(t *testing.T) {
	const certPEM = `-----BEGIN CERTIFICATE-----
MIIBhTCCASugAwIBAgIQIRi6zePL6mKjOipn+dNuaTAKBggqhkjOPQQDAjASMRAw
DgYDVQQKEwdBY21lIENvMB4XDTE3MTAyMDE5NDMwNloXDTE4MTAyMDE5NDMwNlow
EjEQMA4GA1UEChMHQWNtZSBDbzBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABD0d
7VNhbWvZLWPuj/RtHFjvtJBEwOkhbN/BnnE8rnZR8+sbwnc/KhCk3FhnpHZnQz7B
5aETbbIgmuvewdjvSBSjYzBhMA4GA1UdDwEB/wQEAwICpDATBgNVHSUEDDAKBggr
BgEFBQcDATAPBgNVHRMBAf8EBTADAQH/MCkGA1UdEQQiMCCCDmxvY2FsaG9zdDo1
NDUzgg4xMjcuMC4wLjE6NTQ1MzAKBggqhkjOPQQDAgNIADBFAiEA2zpJEPQyz6/l
Wf86aX6PepsntZv2GYlA5UpabfT2EZICICpJ5h/iI+i341gBmLiAFQOyTDT+/wQc
6MF9+Yw1Yy0t
-----END CERTIFICATE-----
`
	core := corefake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "tls", Namespace: "seaweedfs"},
		Data: map[string][]byte{
			"tls.crt": []byte(certPEM),
			"tls.key": []byte("priv"),
			"ca.crt":  []byte(certPEM),
		},
	})

	c := &config{}
	c.Kube.Namespace = "seaweedfs"
	c.MTLS.SecretName = "tls"

	dir := t.TempDir()
	args, err := materializeMTLS(context.Background(), core, c, dir)
	if err != nil {
		t.Fatalf("materializeMTLS: %v", err)
	}
	for _, fn := range []string{"cert.pem", "key.pem", "ca.pem"} {
		if _, err := os.Stat(filepath.Join(dir, fn)); err != nil {
			t.Errorf("expected %s on disk: %v", fn, err)
		}
	}
	flat := strings.Join(args, " ")
	for _, want := range []string{"-volume.cert.file=", "-volume.key.file=", "-volume.ca.file="} {
		if !strings.Contains(flat, want) {
			t.Errorf("flag missing: %s\nargs: %s", want, flat)
		}
	}
}

func TestMaterializeMTLS_NoOpWhenSecretNameEmpty(t *testing.T) {
	core := corefake.NewSimpleClientset()
	c := &config{}
	args, err := materializeMTLS(context.Background(), core, c, "")
	if err != nil {
		t.Fatalf("materializeMTLS: %v", err)
	}
	if args != nil {
		t.Errorf("expected nil args, got %v", args)
	}
}
