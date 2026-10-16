package native

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/appmana/labcontainers/pkg/client"
)

// The candidate's "Volume servers" configuration on a real DSM: the upgraded
// single server becomes two servers through the same --set-servers call the
// wizard's apply step makes, the administrator registration creates the new
// directory and sets the summed memory limit, and both servers join the real
// cluster as separate data nodes of one rack. Original data is read back
// across the change, a restart and an abrupt VM crash.
func TestLiveDSMVolumeServers(t *testing.T) {
	if os.Getenv("DSM_KUBERNETES_INPUTS") == "" {
		t.Skip("requires explicit pinned offline Kubernetes inputs and fresh private DSM disks")
	}
	runDSMKubernetesJoin(t, dsmUpgradeFromEnv(t), qualifyDSMVolumeServers(t))
}

const (
	dsmServerAMemoryMB = 1024
	dsmServerBMemoryMB = 768
	// each server's budget plus the package's 1024 MiB page cache per server
	dsmServersMemoryLimit = (dsmServerAMemoryMB + dsmServerBMemoryMB + 2*1024) << 20
)

func qualifyDSMVolumeServers(t *testing.T) dsmJoinedTransition {
	return func(ctx context.Context, lab *client.Session, cluster func(args ...string) []byte) {
		t.Helper()
		dsmRoot(t, ctx, lab, `set -eu
test -f /root/seaweedfs-private-image
pkg=/var/packages/seaweedfs
first=$(sed -n 's/.*"dir":"\([^"]*\)".*/\1/p' "$pkg/var/volume.yaml")
test -n "$first"
second="${first}-b"
test ! -e "$second"
/usr/syno/bin/synopkg stop seaweedfs
"$pkg/target/bin/synology-volume-bootstrap" --config "$pkg/var/volume.yaml" --set-servers "$first=`+itoa(dsmServerAMemoryMB)+`,$second=`+itoa(dsmServerBMemoryMB)+`"
"$pkg/target/bin/synology-volume-bootstrap" --config "$pkg/var/volume.yaml" --print-servers
sh "$pkg/target/bin/register-service.sh"
for d in "$first" "$second"; do
  test -d "$d"
  test "$(stat -c %U "$d")" = sc-seaweedfs
done
test "$(systemctl show pkg-seaweedfs-volume.service -p MemoryLimit)" = "MemoryLimit=`+itoa(dsmServersMemoryLimit)+`"
/usr/syno/bin/synopkg start seaweedfs
echo DSM_VOLUME_SERVERS_CONFIGURED
`)
		assertDSMVolumeServers(t, ctx, lab, cluster)
	}
}

// Two package processes with distinct directories and ports, each with its own
// Go budget, in one cgroup with the summed limit, both answering and both
// registered as data nodes of the lab rack; the first kept its port.
func assertDSMVolumeServers(t *testing.T, ctx context.Context, lab *client.Session, cluster func(args ...string) []byte) {
	t.Helper()
	out := dsmRoot(t, ctx, lab, `set -eu
uid=$(id -u sc-seaweedfs)
n=0
while :; do
  pids=
  for pid in $(pidof weed); do
    test "$(stat -c %u /proc/$pid)" = "$uid" && pids="$pids $pid"
  done
  set -- $pids
  test "$#" = 2 && break
  n=$((n+1)); test "$n" -lt 60
  sleep 2
done
for pid in $pids; do
  cg=$(awk -F: '$2 == "memory" {print $3}' /proc/$pid/cgroup)
  test "$cg" = /seaweedfs.slice/pkg-seaweedfs-volume.service
  test "$(cat /sys/fs/cgroup/memory$cg/memory.limit_in_bytes)" = `+itoa(dsmServersMemoryLimit)+`
  args=$(tr '\000' ' ' < /proc/$pid/cmdline)
  mem=$(tr '\000' '\n' < /proc/$pid/environ | sed -n 's/^GOMEMLIMIT=//p')
  dir=$(printf '%s\n' $args | sed -n 's/^-dir=//p')
  port=$(printf '%s\n' $args | sed -n 's/^-port=//p')
  grpc=$(printf '%s\n' $args | sed -n 's/^-port.grpc=//p')
  printf 'DSM_VOLUME_SERVER pid=%s dir=%s port=%s grpc=%s gomemlimit=%s\n' "$pid" "$dir" "$port" "$grpc" "$mem"
done
for port in 8080 8081; do
  n=0
  until curl -fsS -m 5 "http://127.0.0.1:$port/status" >/dev/null; do
    n=$((n+1)); test "$n" -lt 60
    sleep 2
  done
done
echo DSM_VOLUME_SERVERS_ANSWER
`)
	type server struct{ dir, port, grpc, memory string }
	byPort := map[string]server{}
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "DSM_VOLUME_SERVER ") {
			continue
		}
		f := map[string]string{}
		for _, kv := range strings.Fields(line)[1:] {
			k, v, _ := strings.Cut(kv, "=")
			f[k] = v
		}
		byPort[f["port"]] = server{f["dir"], f["port"], f["grpc"], f["gomemlimit"]}
	}
	first, second := byPort["8080"], byPort["8081"]
	if first.grpc != "18080" || first.memory != itoa(dsmServerAMemoryMB)+"MiB" {
		t.Fatalf("the first server must keep its ports and take its own budget: %+v", byPort)
	}
	if second.grpc != "18081" || second.memory != itoa(dsmServerBMemoryMB)+"MiB" || second.dir != first.dir+"-b" {
		t.Fatalf("the second server must run from its own directory and ports with its own budget: %+v", byPort)
	}
	var status struct {
		Topology struct {
			DataCenters []struct {
				Id    string
				Racks []struct {
					Id        string
					DataNodes []struct{ Url string }
				}
			}
		}
	}
	limit, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	for {
		nodes := map[string]string{}
		if err := json.Unmarshal(cluster("curl", "-fsS", "http://192.0.2.30:9333/dir/status"), &status); err == nil {
			for _, dc := range status.Topology.DataCenters {
				for _, rack := range dc.Racks {
					for _, node := range rack.DataNodes {
						nodes[node.Url] = dc.Id + "/" + rack.Id
					}
				}
			}
		}
		if nodes["192.0.2.20:8080"] == "lab/synology" && nodes["192.0.2.20:8081"] == "lab/synology" {
			t.Log("DSM_VOLUME_SERVERS_PASS: two data nodes in rack lab/synology, separate directories, ports and budgets, summed package memory limit")
			return
		}
		select {
		case <-limit.Done():
			t.Fatalf("both servers must register as data nodes of rack lab/synology: %v", nodes)
		case <-time.After(3 * time.Second):
		}
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
