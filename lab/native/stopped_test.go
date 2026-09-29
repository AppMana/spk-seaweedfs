package native

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/appmana/labcontainers/pkg/client"
)

func validateStoppedUnit(output string) error {
	want := map[string]string{"LoadState": "loaded", "ActiveState": "inactive", "SubState": "dead", "Result": "success", "MainPID": "0"}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || seen[key] {
			return fmt.Errorf("malformed/duplicate unit property %q", line)
		}
		seen[key] = true
		if expected, required := want[key]; required && value != expected {
			return fmt.Errorf("%s=%s, expected %s", key, value, expected)
		}
	}
	for key := range want {
		if !seen[key] {
			return fmt.Errorf("missing unit property %s", key)
		}
	}
	return nil
}

func TestDSMStoppedUnitOracle(t *testing.T) {
	good := "LoadState=loaded\nActiveState=inactive\nSubState=dead\nResult=success\nMainPID=0\n"
	if err := validateStoppedUnit(good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"unknown\n", "", strings.ReplaceAll(good, "loaded", "not-found"),
		strings.ReplaceAll(good, "inactive", "active"), strings.ReplaceAll(good, "dead", "failed"),
		strings.ReplaceAll(good, "success", "exit-code"), strings.ReplaceAll(good, "MainPID=0", "MainPID=123"),
		strings.ReplaceAll(good, "MainPID=0\n", ""), good + "ActiveState=active\n",
	} {
		if err := validateStoppedUnit(bad); err == nil {
			t.Errorf("accepted invalid stopped state %q", bad)
		}
	}
}

func assertDSMStopped(t *testing.T, ctx context.Context, lab *client.Session) {
	t.Helper()
	// DSM systemd 219 can unload inactive units between calls: is-active
	// then prints unknown. show loads the installed unit; unknown is never
	// accepted as evidence of a successful stop.
	state := dsmRoot(t, ctx, lab, "set -eu\nsystemctl show pkg-seaweedfs-volume.service -p LoadState -p ActiveState -p SubState -p Result -p MainPID\n")
	if err := validateStoppedUnit(state); err != nil {
		t.Fatal("native unit did not stop cleanly:", err)
	}
	dsmRoot(t, ctx, lab, `set -eu
test -f /root/seaweedfs-private-image
uid=$(id -u sc-seaweedfs)
test "$uid" -gt 0
for process in /proc/[0-9]*; do
  owner=$(stat -c %u "$process" 2>/dev/null) || continue
  if test "$owner" = "$uid"; then
    echo "package process survived stop: $process" >&2
    exit 1
  fi
done
group=/sys/fs/cgroup/memory/seaweedfs.slice/pkg-seaweedfs-volume.service
if test -d "$group"; then
  for tasks in $(find "$group" -name cgroup.procs); do
    test -z "$(cat "$tasks")" || { echo 'package cgroup survived stop' >&2; exit 1; }
  done
fi
echo DSM_STOPPED_NO_SURVIVORS_PASS
`)
}
