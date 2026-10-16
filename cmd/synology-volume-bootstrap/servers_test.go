package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func serversBase() *config {
	c := &config{}
	c.Kube.APIServer = "https://api:6443"
	c.Kube.TokenFile = "/t"
	c.Kube.Namespace = "ns"
	c.Kube.MasterService = "seaweedfs-master"
	c.Volume.IP = "192.0.2.10"
	c.Volume.Port = 8080
	c.Volume.GRPCPort = 18080
	c.Volume.MetricsPort = 9325
	c.Volume.Max = 2000
	c.Volume.Rack = "nas"
	c.Volume.Servers = []serverConfig{
		{Dir: "/volume1/seaweed", MemoryMB: 3072},
		{Dir: "/volume2/seaweed", Max: 400},
		{Dir: "/volume3/seaweed", Port: 8090, GRPCPort: 18090, MetricsPort: 9390},
	}
	return c
}

func TestServers_EachServerRunsFromItsOwnDirPortsAndMemory(t *testing.T) {
	c := serversBase()
	if err := validate(c); err != nil {
		t.Fatal(err)
	}
	if got := serverCount(c); got != 3 {
		t.Fatalf("serverCount = %d, want 3", got)
	}
	type want struct {
		dir                 string
		port, grpc, metrics int
		max, memoryMB       int
	}
	for i, w := range []want{
		{"/volume1/seaweed", 8080, 18080, 9325, 2000, 3072},
		{"/volume2/seaweed", 8081, 18081, 9326, 400, defaultServerMemoryMB},
		{"/volume3/seaweed", 8090, 18090, 9390, 2000, defaultServerMemoryMB},
	} {
		s := instanceConfig(c, i)
		v := s.Volume
		if v.Dir != w.dir || v.Port != w.port || v.GRPCPort != w.grpc || v.MetricsPort != w.metrics || v.Max != w.max {
			t.Errorf("server %d: %+v, want %+v", i, v, w)
		}
		if v.PublicURL != "192.0.2.10:"+itoa(w.port) {
			t.Errorf("server %d publicUrl %q", i, v.PublicURL)
		}
		if got := serverMemoryMB(c, i); got != w.memoryMB {
			t.Errorf("server %d memory %d, want %d", i, got, w.memoryMB)
		}
		args := strings.Join(renderArgs(s, "m:9333", nil), " ")
		for _, flag := range []string{"-dir=" + w.dir, "-port=" + itoa(w.port), "-port.grpc=" + itoa(w.grpc), "-metricsPort=" + itoa(w.metrics), "-max=" + itoa(w.max), "-rack=nas"} {
			if !strings.Contains(args, flag) {
				t.Errorf("server %d argv %q lacks %s", i, args, flag)
			}
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestServers_RejectAmbiguousLayouts(t *testing.T) {
	for name, mutate := range map[string]func(c *config){
		"servers and instances":  func(c *config) { c.Volume.Instances = 2 },
		"servers and volume.dir": func(c *config) { c.Volume.Dir = "/volume1/seaweed" },
		"relative dir":           func(c *config) { c.Volume.Servers[1].Dir = "seaweed" },
		"duplicate dir":          func(c *config) { c.Volume.Servers[1].Dir = "/volume1/seaweed/" },
		"nested dir":             func(c *config) { c.Volume.Servers[1].Dir = "/volume1/seaweed/two" },
		"duplicate port":         func(c *config) { c.Volume.Servers[2].Port = 8081 },
		"port equals grpc":       func(c *config) { c.Volume.Servers[2].GRPCPort = 8080 },
		"duplicate metrics":      func(c *config) { c.Volume.Servers[2].MetricsPort = 9326 },
		"tiny memory":            func(c *config) { c.Volume.Servers[1].MemoryMB = 128 },
		"per-server flag shared": func(c *config) { c.Volume.ExtraFlags = []string{"-metricsPort=9325"} },
		"dir flag shared":        func(c *config) { c.Volume.ExtraFlags = []string{"-dir=/x"} },
		"too many": func(c *config) {
			for i := 0; i < 6; i++ {
				c.Volume.Servers = append(c.Volume.Servers, serverConfig{Dir: "/volume9/s" + itoa(i)})
			}
		},
	} {
		c := serversBase()
		mutate(c)
		if err := validate(c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The package cgroup holds every server's Go heap plus the page cache it
// charges; the limit is their sum, not a constant shared by any count.
func TestServers_PackageMemoryLimitIsTheSumOfServerBudgets(t *testing.T) {
	c := serversBase()
	if err := validate(c); err != nil {
		t.Fatal(err)
	}
	want := 3072 + 2*defaultServerMemoryMB + 3*pageCacheMBPerServer
	if got := packageMemoryLimitMB(c); got != want {
		t.Fatalf("packageMemoryLimitMB = %d, want %d", got, want)
	}
	legacy := serversBase()
	legacy.Volume.Servers = nil
	legacy.Volume.Dir = "/volume1/seaweed"
	if err := validate(legacy); err != nil {
		t.Fatal(err)
	}
	if got := packageMemoryLimitMB(legacy); got != 0 {
		t.Fatalf("a single legacy server keeps the unit's own limit, got %d", got)
	}
	if got := serverMemoryMB(legacy, 0); got != 0 {
		t.Fatalf("a single legacy server sizes itself from the cgroup, got %d", got)
	}
}

func TestBootstrap_WritesOneGoMemoryLimitPerServer(t *testing.T) {
	c := serversBase()
	if err := validate(c); err != nil {
		t.Fatal(err)
	}
	run := t.TempDir()
	if err := writeServerMemoryLimits(c, filepath.Join(run, "argv")); err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]string{"gomemlimit": "3072MiB\n", "gomemlimit.1": "1536MiB\n", "gomemlimit.2": "1536MiB\n"} {
		got, err := os.ReadFile(filepath.Join(run, file))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q %v, want %q", file, got, err, want)
		}
	}
}

const wizardYAML = `# operator notes stay
kube:
  apiserver: https://api:6443
  tokenFile: /t
  namespace: ns
  masterService: seaweedfs-master
volume:
  ip: 192.0.2.10
  port: 8080
  grpcPort: 18080
  metricsPort: 9325
  max: 2000
  rack: nas
  extraFlags: ["-compactionMBps=69"]
  servers:
    - dir: /volume1/seaweed
      port: 8080
      grpcPort: 18080
      metricsPort: 9325
      memoryMB: 3072
    - dir: /volume2/seaweed
      port: 8081
      grpcPort: 18081
      metricsPort: 9326
`

// A server's port is its identity in the cluster topology: a kept server must
// keep it, and a new one must take ports no server uses.
func TestSetServers_KeepsIdentityOfRetainedServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "volume.yaml")
	if err := os.WriteFile(path, []byte(wizardYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := setServers(path, "/volume1/seaweed=4096,/volume4/seaweed=2048,/volume3/seaweed"); err != nil {
		t.Fatal(err)
	}
	// as written: explicit ports, budgets only where chosen
	c, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	got := append([]serverConfig(nil), c.Volume.Servers...)
	if err := validate(c); err != nil {
		t.Fatal(err)
	}
	want := []serverConfig{
		{Dir: "/volume1/seaweed", Port: 8080, GRPCPort: 18080, MetricsPort: 9325, MemoryMB: 4096},
		{Dir: "/volume4/seaweed", Port: 8082, GRPCPort: 18082, MetricsPort: 9327, MemoryMB: 2048},
		{Dir: "/volume3/seaweed", Port: 8083, GRPCPort: 18083, MetricsPort: 9328},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("servers = %+v\nwant %+v", got, want)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "# operator notes stay") || !strings.Contains(string(raw), "-compactionMBps=69") {
		t.Fatalf("rewrite dropped unrelated configuration:\n%s", raw)
	}
	var probe map[string]any
	if err := yaml.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	if err := setServers(path, ""); err == nil {
		t.Fatal("an empty server list must be rejected")
	}
	if err := setServers(path, "/volume1/seaweed=lots"); err == nil {
		t.Fatal("a non-numeric memory budget must be rejected")
	}
}

// Moving a single legacy server to a list keeps its data where it is.
func TestSetServers_AdoptsLegacySingleServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "volume.yaml")
	legacy := strings.Replace(wizardYAML, `  servers:
    - dir: /volume1/seaweed
      port: 8080
      grpcPort: 18080
      metricsPort: 9325
      memoryMB: 3072
    - dir: /volume2/seaweed
      port: 8081
      grpcPort: 18081
      metricsPort: 9326
`, "  dir: /volume1/seaweed\n  instances: 1\n", 1)
	// the deployed layout passes the metrics port as an extra flag
	legacy = strings.Replace(legacy, "  metricsPort: 9325\n", "", 1)
	legacy = strings.Replace(legacy, `extraFlags: ["-compactionMBps=69"]`, `extraFlags: ["-compactionMBps=69", "-metricsPort=9325"]`, 1)
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := setServers(path, "/volume1/seaweed=3072,/volume2/seaweed"); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Volume.Dir != "" || c.Volume.Instances != 0 {
		t.Fatalf("legacy layout keys must be replaced by the list: dir=%q instances=%d", c.Volume.Dir, c.Volume.Instances)
	}
	if err := validate(c); err != nil {
		t.Fatal(err)
	}
	if s := c.Volume.Servers; len(s) != 2 || s[0].Port != 8080 || s[0].GRPCPort != 18080 || s[0].MetricsPort != 9325 || s[1].Port != 8081 || s[1].MetricsPort != 9326 {
		t.Fatalf("servers = %+v", s)
	}
	if !reflect.DeepEqual(c.Volume.ExtraFlags, []string{"-compactionMBps=69"}) {
		t.Fatalf("the per-server metrics flag must leave the shared flags: %q", c.Volume.ExtraFlags)
	}
}

// A CLI install has no cluster connection yet; the service scripts and the
// wizard still read where its servers run.
func TestValidateLayout_NeedsNoClusterConfiguration(t *testing.T) {
	c := &config{}
	c.Volume.Servers = []serverConfig{{Dir: "/volume1/seaweed"}, {Dir: "/volume2/seaweed"}}
	if err := validateLayout(c); err != nil {
		t.Fatalf("layout without a cluster connection: %v", err)
	}
	if got := packageMemoryLimitMB(c); got != 2*(defaultServerMemoryMB+pageCacheMBPerServer) {
		t.Fatalf("packageMemoryLimitMB = %d", got)
	}
	if err := validate(c); err == nil {
		t.Fatal("starting a server still requires the cluster connection")
	}
}
