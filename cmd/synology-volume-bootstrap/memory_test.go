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

// goMB is each server's Go soft limit.
func goMB(budgets []serverMemory) []int {
	out := make([]int, len(budgets))
	for i, b := range budgets {
		out[i] = b.GoMB
	}
	return out
}

// Servers without memoryMB hold their indexes, 256 MiB plus 2 MiB per volume,
// and share what remains of the package limit (less the page cache reserve)
// equally for transfers.
func TestMemory_AutomaticBudgetsFollowVolumesHeld(t *testing.T) {
	withHostMemory(t, 7915)
	c := autoServers(withVolumes(t, 742), withVolumes(t, 25), withVolumes(t, 4), withVolumes(t, 5))
	if err := validateLayout(c); err != nil {
		t.Fatal(err)
	}
	// Go total 7915-2048-1024 = 4843 MiB; indexes 1740, 306, 264, 266 of 2576; 2267/4 = 566 each for transfers
	got, err := serverBudgetsMB(c)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(goMB(got)) != "[2306 872 830 832]" {
		t.Fatalf("budgets %v, want [2306 872 830 832]", goMB(got))
	}
}

// Upload admission is not split by the volumes a server holds: the master
// grows new volumes on the emptiest disk, so a nearly empty server takes most
// writes and replicas. 4.48-8 gave the NAS's new /volume4 server (8 volumes) a
// 509 MiB Go limit, from which the core's admission, assuming a 1.25 GiB base
// heap, fell to its floor of 93 MiB; replicate POSTs waited up to 25 s for it
// and the primaries' 5 s no-progress deadline failed a trainer's checkpoint
// (2026-10-26). Each automatic server is given its transfer share as explicit
// admission, upload:download 3:1 as the core splits it.
func TestMemory_TransferAdmissionIsSharedEquallyNotByVolumesHeld(t *testing.T) {
	withHostMemory(t, 7915)
	c := autoServers(withVolumes(t, 743), withVolumes(t, 26), withVolumes(t, 4), withVolumes(t, 8))
	if err := validateLayout(c); err != nil {
		t.Fatal(err)
	}
	got, err := serverBudgetsMB(c)
	if err != nil {
		t.Fatal(err)
	}
	// indexes 1742, 308, 264, 272 of 4843: 2257/4 = 564 each, 423 upload and 141 download
	for i, b := range got {
		if b.UploadMB != 423 || b.DownloadMB != 141 {
			t.Errorf("server %d admission %d/%d MiB, want 423/141", i, b.UploadMB, b.DownloadMB)
		}
	}
	if fmt.Sprint(goMB(got)) != "[2306 872 828 836]" {
		t.Fatalf("budgets %v, want [2306 872 828 836]", goMB(got))
	}
	args := admissionArgs(c, got[3])
	if fmt.Sprint(args) != "[-concurrentUploadLimitMB=423 -concurrentDownloadLimitMB=141]" {
		t.Fatalf("admission args %v", args)
	}
}

// An operator's admission flag in extraFlags wins; a server with an explicit
// memoryMB keeps the core's own derivation.
func TestMemory_ExplicitAdmissionIsKept(t *testing.T) {
	c := autoServers("/volume1/a")
	c.Volume.ExtraFlags = []string{"-concurrentUploadLimitMB=2048"}
	if got := admissionArgs(c, serverMemory{GoMB: 1000, UploadMB: 300, DownloadMB: 100}); fmt.Sprint(got) != "[-concurrentDownloadLimitMB=100]" {
		t.Fatalf("admission args %v", got)
	}
	if got := admissionArgs(autoServers("/volume1/a"), serverMemory{GoMB: 1024}); len(got) != 0 {
		t.Fatalf("an explicit memoryMB leaves admission to the core, got %v", got)
	}
}

// On a small machine the page cache reserve is a quarter of the package
// limit, so a 4 GB DSM still runs a fixed 1024 MiB server beside an
// automatic one.
func TestMemory_SmallMachinesReserveAQuarterForPageCache(t *testing.T) {
	withHostMemory(t, 3918)
	c := autoServers(withVolumes(t, 7), withVolumes(t, 0))
	c.Volume.Servers[0].MemoryMB = 1024
	if err := validateLayout(c); err != nil {
		t.Fatal(err)
	}
	got, err := serverBudgetsMB(c)
	if err != nil {
		t.Fatal(err)
	}
	// limit 3918-2048 = 1870, reserve 1870/4 = 467: 1870-467-1024 = 379
	if fmt.Sprint(goMB(got)) != "[1024 379]" {
		t.Fatalf("budgets %v, want [1024 379]", goMB(got))
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
	if want := fmt.Sprint([]int{1024, rest / 2, rest / 2}); fmt.Sprint(goMB(got)) != want {
		t.Fatalf("budgets %v, want %s", goMB(got), want)
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
