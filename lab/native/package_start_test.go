package native

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDSMInitialPackageStartup(t *testing.T) {
	for _, kind := range []string{"legacy", "registered-unit", "registration-failure"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			pkg := filepath.Join(root, "package with spaces")
			bin := filepath.Join(pkg, "target", "bin")
			if err := os.MkdirAll(bin, 0755); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(root, "registered")
			started := filepath.Join(root, "started")
			write := func(path, body string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if kind != "legacy" {
				registration := "touch " + shellQuote(marker) + "\n"
				if kind == "registration-failure" {
					registration = "exit 42\n"
				}
				write(filepath.Join(bin, "register-service.sh"), registration)
			}
			control := filepath.Join(root, "synopkg")
			check := ""
			if kind != "legacy" {
				check = "test -f " + shellQuote(marker) + "\n"
			}
			write(control, "test \"$1 $2\" = 'start seaweedfs'\n"+check+"touch "+shellQuote(started)+"\n")
			out, err := exec.Command("sh", "-c", dsmStartPackageScript(pkg, control)).CombinedOutput()
			if kind == "registration-failure" {
				if err == nil {
					t.Fatal("failed registration accepted")
				}
				if _, err := os.Stat(started); !os.IsNotExist(err) {
					t.Fatal("started after failed registration")
				}
				return
			}
			if err != nil {
				t.Fatalf("startup failed: %v: %s", err, out)
			}
			if _, err := os.Stat(started); err != nil {
				t.Fatal(err)
			}
		})
	}
}
