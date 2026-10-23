package main

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"
)

// serverConfig is one `weed volume` process of volume.servers: its own data
// directory (normally its own DSM volume), ports and memory budget. Shared
// settings (ip, dataCenter, rack, index, extraFlags, kube) come from volume.
// A server's ports are its identity in the cluster topology, so they are
// written explicitly and never renumbered once assigned.
type serverConfig struct {
	Dir         string `yaml:"dir"`
	Port        int    `yaml:"port,omitempty"`
	GRPCPort    int    `yaml:"grpcPort,omitempty"`
	MetricsPort int    `yaml:"metricsPort,omitempty"`
	Max         int    `yaml:"max,omitempty"`
	DiskType    string `yaml:"diskType,omitempty"`
	// MemoryMB is the server's Go soft limit (GOMEMLIMIT); the core derives
	// its upload/download admission from it. Unset, the server takes a share
	// of the installed memory by the volumes it holds (serverBudgetsMB).
	MemoryMB int `yaml:"memoryMB,omitempty"`
}

const (
	maxServers        = 8
	minServerMemoryMB = 256
	// DSM keeps 2 GiB of the installed memory, or a quarter of a larger
	// machine; the package cgroup limit is the rest.
	hostReserveMinMB   = 2048
	hostReserveDivisor = 4
	// pageCacheReserveMB of the package limit stays outside the Go budgets:
	// cgroup v1 charges page cache to the package, and without headroom every
	// read evicts.
	pageCacheReserveMB = 1024
	// An automatic budget is weighted by what the server holds: a LevelDB
	// index costs about 2 MB per volume (a 746-volume server measured 1.4 GB
	// resident, 2.3 GB peak) over a small base.
	serverBaseMB = 256
	volumeMB     = 2
	// legacySharedMemoryMB is the Go budget volume.instances divides.
	legacySharedMemoryMB = 3072
)

// meminfoPath is /proc/meminfo; tests replace it.
var meminfoPath = "/proc/meminfo"

// perServerFlags are set per server and so cannot be shared extraFlags.
var perServerFlags = map[string]bool{"dir": true, "port": true, "port.grpc": true, "metricsPort": true, "max": true, "publicUrl": true, "disk": true}

func serverCount(c *config) int {
	if len(c.Volume.Servers) > 0 {
		return len(c.Volume.Servers)
	}
	return c.Volume.Instances
}

// validateServers fills defaults into volume.servers and rejects layouts in
// which two servers could share a directory, a port or a per-server flag.
func validateServers(c *config) error {
	v := &c.Volume
	if v.Dir != "" {
		return errors.New("volume.dir and volume.servers are mutually exclusive; give each server its dir")
	}
	if v.Instances > 1 {
		return errors.New("volume.instances and volume.servers are mutually exclusive")
	}
	if len(v.Servers) > maxServers {
		return fmt.Errorf("volume.servers holds at most %d servers, got %d", maxServers, len(v.Servers))
	}
	for _, f := range v.ExtraFlags {
		name, _, _ := strings.Cut(strings.TrimLeft(f, "-"), "=")
		if perServerFlags[name] {
			return fmt.Errorf("volume.extraFlags %q is set per server in volume.servers", f)
		}
	}
	used := map[int]string{}
	claim := func(port int, what string) error {
		if port < 1 || port > 65535 {
			return fmt.Errorf("%s %d out of range", what, port)
		}
		if other, ok := used[port]; ok {
			return fmt.Errorf("%s %d is already %s", what, port, other)
		}
		used[port] = what
		return nil
	}
	var dirs []string
	for i := range v.Servers {
		s := &v.Servers[i]
		if !path.IsAbs(s.Dir) {
			return fmt.Errorf("volume.servers[%d].dir must be absolute, got %q", i, s.Dir)
		}
		s.Dir = path.Clean(s.Dir)
		for _, d := range dirs {
			if d == s.Dir || strings.HasPrefix(s.Dir+"/", d+"/") || strings.HasPrefix(d+"/", s.Dir+"/") {
				return fmt.Errorf("volume.servers[%d].dir %s overlaps %s", i, s.Dir, d)
			}
		}
		dirs = append(dirs, s.Dir)
		if s.Port == 0 {
			s.Port = v.Port + i
		}
		if s.GRPCPort == 0 {
			s.GRPCPort = v.GRPCPort + i
		}
		if s.MetricsPort == 0 && v.MetricsPort > 0 {
			s.MetricsPort = v.MetricsPort + i
		}
		if s.Max == 0 {
			s.Max = v.Max
		}
		if s.DiskType == "" {
			s.DiskType = v.DiskType
		}
		if s.MemoryMB != 0 && s.MemoryMB < minServerMemoryMB {
			return fmt.Errorf("volume.servers[%d].memoryMB must be at least %d", i, minServerMemoryMB)
		}
		if err := claim(s.Port, fmt.Sprintf("server %d port", i)); err != nil {
			return err
		}
		if err := claim(s.GRPCPort, fmt.Sprintf("server %d grpcPort", i)); err != nil {
			return err
		}
		if s.MetricsPort > 0 {
			if err := claim(s.MetricsPort, fmt.Sprintf("server %d metricsPort", i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// hostMemoryMB is the installed memory, MemTotal.
func hostMemoryMB() (int, error) {
	raw, err := os.ReadFile(meminfoPath)
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if rest, ok := strings.CutPrefix(line, "MemTotal:"); ok {
			f := strings.Fields(rest)
			if len(f) == 2 && f[1] == "kB" {
				kb, err := strconv.Atoi(f[0])
				if err == nil && kb > 0 {
					return kb / 1024, nil
				}
			}
			return 0, fmt.Errorf("%s: unreadable MemTotal %q", meminfoPath, line)
		}
	}
	return 0, fmt.Errorf("%s has no MemTotal", meminfoPath)
}

// packageMemoryLimitMB is the package cgroup limit volume.servers runs in,
// the installed memory less DSM's share, or 0 to keep the unit's own limit.
// Explicit budgets that cannot fit in it are refused.
func packageMemoryLimitMB(c *config) (int, error) {
	if len(c.Volume.Servers) == 0 {
		return 0, nil
	}
	host, err := hostMemoryMB()
	if err != nil {
		return 0, err
	}
	limit := host - max(hostReserveMinMB, host/hostReserveDivisor)
	explicit, auto := 0, 0
	for _, s := range c.Volume.Servers {
		if s.MemoryMB == 0 {
			auto++
		}
		explicit += s.MemoryMB
	}
	if need := explicit + auto*minServerMemoryMB + pageCacheReserveMB; need > limit {
		return 0, fmt.Errorf("memory budgets of %d MiB plus %d MiB page cache exceed the package's %d MiB of %d MiB installed",
			explicit+auto*minServerMemoryMB, pageCacheReserveMB, limit, host)
	}
	return limit, nil
}

// volumesHeld counts the volumes in a server's directory, normal and erasure
// coded; a missing directory holds none.
func volumesHeld(dir string) int {
	entries, _ := os.ReadDir(dir)
	n := 0
	for _, e := range entries {
		if ext := filepath.Ext(e.Name()); !e.IsDir() && (ext == ".dat" || ext == ".ecx") {
			n++
		}
	}
	return n
}

// serverBudgetsMB is each server's Go soft limit, or nil to let the core size
// a lone process from the cgroup. Explicit memoryMB values are kept; the other
// servers share the package limit less the page cache reserve and the explicit
// budgets, weighted by the volumes each holds when the package starts.
func serverBudgetsMB(c *config) ([]int, error) {
	if len(c.Volume.Servers) == 0 {
		if c.Volume.Instances > 1 {
			budgets := make([]int, c.Volume.Instances)
			for i := range budgets {
				budgets[i] = legacySharedMemoryMB / c.Volume.Instances
			}
			return budgets, nil
		}
		return nil, nil
	}
	limit, err := packageMemoryLimitMB(c)
	if err != nil {
		return nil, err
	}
	share := limit - pageCacheReserveMB
	weights := make([]int, len(c.Volume.Servers))
	total := 0
	for i, s := range c.Volume.Servers {
		if s.MemoryMB != 0 {
			share -= s.MemoryMB
			continue
		}
		weights[i] = serverBaseMB + volumeMB*volumesHeld(s.Dir)
		total += weights[i]
	}
	budgets := make([]int, len(c.Volume.Servers))
	for i, s := range c.Volume.Servers {
		if s.MemoryMB != 0 {
			budgets[i] = s.MemoryMB
			continue
		}
		budgets[i] = int(int64(share) * int64(weights[i]) / int64(total))
		if budgets[i] < minServerMemoryMB {
			return nil, fmt.Errorf("volume.servers[%d] (%s) would get %d MiB, below the %d MiB minimum; lower the explicit budgets",
				i, s.Dir, budgets[i], minServerMemoryMB)
		}
	}
	return budgets, nil
}

// writeServerMemoryLimits writes run/gomemlimit[.i] beside the argv files;
// run.sh exports it unless the operator set GOMEMLIMIT. Stale files from a
// larger earlier layout are removed.
func writeServerMemoryLimits(c *config, argvPath string) error {
	dir := filepath.Dir(argvPath)
	stale, _ := filepath.Glob(filepath.Join(dir, "gomemlimit*"))
	for _, f := range stale {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	budgets, err := serverBudgetsMB(c)
	if err != nil {
		return err
	}
	for i, mb := range budgets {
		name := "gomemlimit"
		if i > 0 {
			name = fmt.Sprintf("gomemlimit.%d", i)
		}
		if err := writeArgv(filepath.Join(dir, name), []string{fmt.Sprintf("%dMiB", mb)}); err != nil {
			return err
		}
	}
	return nil
}

// setServers rewrites volume.yaml's server list from the wizard's selection,
// "dir[=memoryMB|auto],...", in order; a server without a value keeps its
// budget and "auto" returns it to automatic sizing. Servers that stay keep their ports; new
// servers take ports above every port used before or after, so no new server
// takes a removed server's identity. A legacy single volume.dir becomes the
// first entry with its ports, and a -metricsPort extra flag becomes
// volume.metricsPort. Everything else in the file is preserved.
func setServers(file, spec string) error {
	type want struct {
		dir string
		mem int
	}
	var wants []want
	for _, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		dir, mem, hasMem := strings.Cut(item, "=")
		w := want{dir: path.Clean(strings.TrimSpace(dir))}
		if hasMem {
			if mem = strings.TrimSpace(mem); mem == "auto" {
				w.mem = -1
			} else {
				n, err := strconv.Atoi(mem)
				if err != nil {
					return fmt.Errorf("memory for %s: %w", dir, err)
				}
				w.mem = n
			}
		}
		wants = append(wants, w)
	}
	if len(wants) == 0 {
		return errors.New("at least one volume server is required")
	}

	raw, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return err
	}
	old, err := loadConfig(file)
	if err != nil {
		return err
	}
	volume := mappingValue(doc.Content[0], "volume")
	if volume == nil {
		return errors.New("volume.yaml has no volume section")
	}

	// existing servers with their effective ports
	base := old.Volume
	if base.Port == 0 {
		base.Port = 8080
	}
	if base.GRPCPort == 0 {
		base.GRPCPort = base.Port + 10000
	}
	var shared []string
	for _, f := range base.ExtraFlags {
		if v, ok := strings.CutPrefix(f, "-metricsPort="); ok && base.MetricsPort == 0 {
			if n, err := strconv.Atoi(v); err == nil {
				base.MetricsPort = n
				continue
			}
		}
		shared = append(shared, f)
	}
	existing := old.Volume.Servers
	if len(existing) == 0 && old.Volume.Dir != "" {
		if old.Volume.Instances > 1 {
			return errors.New("volume.instances > 1 shares one directory; move its v<i> data before listing servers")
		}
		existing = []serverConfig{{Dir: old.Volume.Dir}}
	}
	for i := range existing {
		s := &existing[i]
		s.Dir = path.Clean(s.Dir)
		if s.Port == 0 {
			s.Port = base.Port + i
		}
		if s.GRPCPort == 0 {
			s.GRPCPort = base.GRPCPort + i
		}
		if s.MetricsPort == 0 && base.MetricsPort > 0 {
			s.MetricsPort = base.MetricsPort + i
		}
	}
	maxPort, maxGRPC, maxMetrics := base.Port-1, base.GRPCPort-1, base.MetricsPort-1
	for _, s := range existing {
		maxPort, maxGRPC, maxMetrics = max(maxPort, s.Port), max(maxGRPC, s.GRPCPort), max(maxMetrics, s.MetricsPort)
	}
	var servers []serverConfig
	for _, w := range wants {
		var s *serverConfig
		for i := range existing {
			if existing[i].Dir == w.dir {
				kept := existing[i]
				s = &kept
			}
		}
		if s == nil {
			maxPort, maxGRPC = maxPort+1, maxGRPC+1
			s = &serverConfig{Dir: w.dir, Port: maxPort, GRPCPort: maxGRPC}
			if base.MetricsPort > 0 {
				maxMetrics++
				s.MetricsPort = maxMetrics
			}
		}
		switch {
		case w.mem < 0:
			s.MemoryMB = 0
		case w.mem > 0:
			s.MemoryMB = w.mem
		}
		servers = append(servers, *s)
	}

	deleteMappingKey(volume, "dir")
	deleteMappingKey(volume, "instances")
	if base.MetricsPort > 0 && old.Volume.MetricsPort == 0 {
		setMappingValue(volume, "metricsPort", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(base.MetricsPort)})
		var flags yaml.Node
		if err := flags.Encode(shared); err != nil {
			return err
		}
		flags.Style = yaml.FlowStyle
		setMappingValue(volume, "extraFlags", &flags)
	}
	var list yaml.Node
	if err := list.Encode(servers); err != nil {
		return err
	}
	setMappingValue(volume, "servers", &list)

	var out strings.Builder
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	var check config
	if err := yaml.Unmarshal([]byte(out.String()), &check); err != nil {
		return err
	}
	if err := validateLayout(&check); err != nil {
		return fmt.Errorf("rewritten configuration is invalid: %w", err)
	}
	info, err := os.Stat(file)
	if err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, []byte(out.String()), info.Mode().Perm()); err != nil {
		return err
	}
	// The rename replaces the file with one the caller owns; an administrator
	// running this as root must not leave the package user unable to read it.
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		if err := chownFile(tmp, int(st.Uid), int(st.Gid)); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	return os.Rename(tmp, file)
}

// chownFile is os.Chown; tests observe it.
var chownFile = os.Chown

func mappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func setMappingValue(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)
}

func deleteMappingKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}
