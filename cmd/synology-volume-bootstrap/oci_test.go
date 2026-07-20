package main

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"log"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

// tarLayer builds a single-layer tar with the given path->content files.
func tarLayer(t *testing.T, files map[string]string) v1.Layer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for p, content := range files {
		hdr := &tar.Header{Name: p, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if content == "" && p[len(p)-1] == '/' {
			hdr.Typeflag = tar.TypeDir
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := io.WriteString(tw, content); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	layer, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return layer
}

func imageFromLayers(t *testing.T, layers ...v1.Layer) v1.Image {
	t.Helper()
	img := empty.Image
	for _, l := range layers {
		var err error
		img, err = mutate.AppendLayers(img, l)
		if err != nil {
			t.Fatal(err)
		}
	}
	return img
}

// startRegistry serves an in-memory OCI registry and pushes img to
// <host>/test/weed:v1. Returns the image reference and the digest.
func startRegistry(t *testing.T, img v1.Image) (srv *httptest.Server, ref string, digest v1.Hash) {
	t.Helper()
	srv = httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	ref = u.Host + "/test/weed:v1"
	if err := crane.Push(img, ref, crane.Insecure); err != nil {
		t.Fatal(err)
	}
	digest, err = img.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return srv, ref, digest
}

func ociConfig(image, cacheDir string) *config {
	c := &config{}
	c.Weed.Image = image
	c.Weed.PlainHTTP = true
	c.Weed.CacheDir = cacheDir
	return c
}

func TestMaterializeWeed_NoImageNoOp(t *testing.T) {
	c := &config{}
	got, err := materializeWeed(context.Background(), c)
	if err != nil || got != "" {
		t.Fatalf("expected no-op, got %q, %v", got, err)
	}
}

func TestMaterializeWeed_ExtractsBinary(t *testing.T) {
	img := imageFromLayers(t,
		tarLayer(t, map[string]string{"etc/motd": "hi"}),
		tarLayer(t, map[string]string{"usr/bin/weed": "#!/bin/true weed-fork"}),
	)
	srv, ref, digest := startRegistry(t, img)
	defer srv.Close()

	cache := t.TempDir()
	got, err := materializeWeed(context.Background(), ociConfig(ref, cache))
	if err != nil {
		t.Fatalf("materializeWeed: %v", err)
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if string(data) != "#!/bin/true weed-fork" {
		t.Errorf("extracted content = %q", data)
	}
	fi, _ := os.Stat(got)
	if fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("extracted binary not executable: %v", fi.Mode())
	}
	wantDir := "sha256-" + digest.Hex
	if filepath.Base(filepath.Dir(got)) != wantDir {
		t.Errorf("cache dir = %s, want %s", filepath.Dir(got), wantDir)
	}
	if _, err := os.Readlink(filepath.Join(cache, "current")); err != nil {
		t.Errorf("current symlink: %v", err)
	}
}

func TestMaterializeWeed_TopLayerWins(t *testing.T) {
	img := imageFromLayers(t,
		tarLayer(t, map[string]string{"usr/bin/weed": "old"}),
		tarLayer(t, map[string]string{"usr/bin/weed": "new"}),
	)
	srv, ref, _ := startRegistry(t, img)
	defer srv.Close()

	got, err := materializeWeed(context.Background(), ociConfig(ref, t.TempDir()))
	if err != nil {
		t.Fatalf("materializeWeed: %v", err)
	}
	if data, _ := os.ReadFile(got); string(data) != "new" {
		t.Errorf("extracted %q, want top-layer content", data)
	}
}

func TestMaterializeWeed_WhiteoutHides(t *testing.T) {
	img := imageFromLayers(t,
		tarLayer(t, map[string]string{"usr/bin/weed": "old"}),
		tarLayer(t, map[string]string{"usr/bin/.wh.weed": ""}),
	)
	srv, ref, _ := startRegistry(t, img)
	defer srv.Close()

	_, err := materializeWeed(context.Background(), ociConfig(ref, t.TempDir()))
	if err == nil || !contains(err.Error(), "not found in image") {
		t.Fatalf("expected not-found error for whiteout'd path, got %v", err)
	}
}

func TestMaterializeWeed_DigestPinMismatch(t *testing.T) {
	img := imageFromLayers(t, tarLayer(t, map[string]string{"usr/bin/weed": "x"}))
	srv, ref, _ := startRegistry(t, img)
	defer srv.Close()

	c := ociConfig(ref, t.TempDir())
	c.Weed.Digest = "sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	_, err := materializeWeed(context.Background(), c)
	if err == nil || !contains(err.Error(), "digest mismatch") {
		t.Fatalf("expected digest mismatch, got %v", err)
	}
}

func TestMaterializeWeed_CacheHitOffline(t *testing.T) {
	img := imageFromLayers(t, tarLayer(t, map[string]string{"usr/bin/weed": "cached"}))
	srv, ref, digest := startRegistry(t, img)

	cache := t.TempDir()
	c := ociConfig(ref, cache)
	if _, err := materializeWeed(context.Background(), c); err != nil {
		t.Fatalf("first pull: %v", err)
	}
	srv.Close() // registry gone

	// Digest pin: cache key computable offline.
	c.Weed.Digest = digest.String()
	got, err := materializeWeed(context.Background(), c)
	if err != nil {
		t.Fatalf("offline cache hit with pin: %v", err)
	}
	if data, _ := os.ReadFile(got); string(data) != "cached" {
		t.Errorf("cache content = %q", data)
	}

	// No pin: tag resolution fails, falls back to `current`.
	c.Weed.Digest = ""
	got, err = materializeWeed(context.Background(), c)
	if err != nil {
		t.Fatalf("offline fallback to current: %v", err)
	}
	if data, _ := os.ReadFile(got); string(data) != "cached" {
		t.Errorf("fallback content = %q", data)
	}
}

func TestMaterializeWeed_MultipleBinaries(t *testing.T) {
	img := imageFromLayers(t, tarLayer(t, map[string]string{
		"usr/bin/weed":        "go-weed",
		"usr/bin/weed-volume": "rust-weed",
	}))
	srv, ref, _ := startRegistry(t, img)
	defer srv.Close()

	c := ociConfig(ref, t.TempDir())
	c.Weed.Binaries = []string{"/usr/bin/weed", "/usr/bin/weed-volume"}
	got, err := materializeWeed(context.Background(), c)
	if err != nil {
		t.Fatalf("materializeWeed: %v", err)
	}
	if filepath.Base(got) != "weed" {
		t.Errorf("returned %s, want the binary named weed", got)
	}
	if data, _ := os.ReadFile(filepath.Join(filepath.Dir(got), "weed-volume")); string(data) != "rust-weed" {
		t.Errorf("weed-volume content = %q", data)
	}
}

func contains(s, sub string) bool {
	return bytes.Contains([]byte(s), []byte(sub))
}
