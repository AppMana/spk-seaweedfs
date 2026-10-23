package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withHostMemory points the bootstrap at a meminfo reporting mib MiB.
func withHostMemory(t *testing.T, mib int) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "meminfo")
	content := fmt.Sprintf("MemTotal:       %d kB\nMemFree:          356476 kB\n", mib*1024)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	saved := meminfoPath
	meminfoPath = path
	t.Cleanup(func() { meminfoPath = saved })
}

// withVolumes creates a server directory holding n volumes (n .dat files,
// with the .idx beside each that must not count twice).
func withVolumes(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	for i := 1; i <= n; i++ {
		for _, ext := range []string{".dat", ".idx"} {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("c_%d%s", i, ext)), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

func autoServers(dirs ...string) *config {
	c := serversBase()
	c.Volume.Servers = nil
	for _, d := range dirs {
		c.Volume.Servers = append(c.Volume.Servers, serverConfig{Dir: d})
	}
	return c
}

// The package limit follows the installed memory, keeping DSM's share:
// 2 GiB, or a quarter of a larger machine.
func TestMemory_PackageLimitFollowsInstalledMemory(t *testing.T) {
	for _, tc := range []struct{ host, want int }{
		{7915, 7915 - 2048},   // the NAS on its 8 GB DIMM
		{16384, 16384 - 4096}, // 16 GB
		{4096, 4096 - 2048},
	} {
		withHostMemory(t, tc.host)
		c := autoServers("/volume1/a", "/volume2/b")
		if err := validateLayout(c); err != nil {
			t.Fatal(err)
		}
		got, err := packageMemoryLimitMB(c)
		if err != nil || got != tc.want {
			t.Errorf("host %d MiB: packageMemoryLimitMB = %d %v, want %d", tc.host, got, err, tc.want)
		}
	}
}

// Servers without memoryMB share the package limit, less the page cache
// reserve, by the volumes each holds: 256 MiB plus 2 MiB per volume.
func TestMemory_AutomaticBudgetsFollowVolumesHeld(t *testing.T) {
	withHostMemory(t, 7915)
	c := autoServers(withVolumes(t, 742), withVolumes(t, 25), withVolumes(t, 4), withVolumes(t, 5))
	if err := validateLayout(c); err != nil {
		t.Fatal(err)
	}
	// Go total 7915-2048-1024 = 4843 MiB; weights 1740, 306, 264, 266 of 2576
	want := []int{4843 * 1740 / 2576, 4843 * 306 / 2576, 4843 * 264 / 2576, 4843 * 266 / 2576}
	got, err := serverBudgetsMB(c)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("budgets %v, want %v", got, want)
	}
	if fmt.Sprint(want) != "[3271 575 496 500]" {
		t.Fatalf("worked example changed: %v", want)
	}
}

// An explicit memoryMB is kept; automatic servers share what remains.
func TestMemory_ExplicitBudgetsAreKeptAndAutomaticOnesShareTheRest(t *testing.T) {
	withHostMemory(t, 7915)
	c := autoServers(withVolumes(t, 10), withVolumes(t, 0), withVolumes(t, 0))
	c.Volume.Servers[0].MemoryMB = 1024
	if err := validateLayout(c); err != nil {
		t.Fatal(err)
	}
	got, err := serverBudgetsMB(c)
	if err != nil {
		t.Fatal(err)
	}
	rest := 4843 - 1024
	if want := fmt.Sprint([]int{1024, rest / 2, rest / 2}); fmt.Sprint(got) != want {
		t.Fatalf("budgets %v, want %s", got, want)
	}
}

// Budgets that cannot fit the installed memory are refused, the way the
// 16 GB layout (3072 + 3x1536 MiB) cannot run on 8 GB.
func TestMemory_BudgetsBeyondInstalledMemoryAreRefused(t *testing.T) {
	withHostMemory(t, 7915)
	c := autoServers("/volume1/a", "/volume2/b", "/volume3/c", "/volume4/d")
	for i, mb := range []int{3072, 1536, 1536, 1536} {
		c.Volume.Servers[i].MemoryMB = mb
	}
	if err := validateLayout(c); err != nil {
		t.Fatal(err)
	}
	if _, err := packageMemoryLimitMB(c); err == nil || !strings.Contains(err.Error(), "7680 MiB") {
		t.Fatalf("an explicit layout beyond the installed memory must be refused, got %v", err)
	}
	// an automatic server still needs its minimum
	c = autoServers("/volume1/a", "/volume2/b")
	c.Volume.Servers[0].MemoryMB = 4843
	if _, err := serverBudgetsMB(c); err == nil {
		t.Fatal("an automatic server left below its minimum must be refused")
	}
}

// The wizard reads the configured value (0: automatic) beside the budget the
// server runs with; "auto" returns a server to automatic sizing.
func TestSetServers_AutoClearsAnExplicitBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "volume.yaml")
	if err := os.WriteFile(path, []byte(wizardYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := setServers(path, "/volume1/seaweed=auto,/volume2/seaweed=2048"); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if s := c.Volume.Servers; len(s) != 2 || s[0].MemoryMB != 0 || s[1].MemoryMB != 2048 {
		t.Fatalf("servers = %+v", s)
	}
	raw, _ := os.ReadFile(path)
	if strings.Count(string(raw), "memoryMB") != 1 {
		t.Fatalf("an automatic server must not write memoryMB:\n%s", raw)
	}
}
