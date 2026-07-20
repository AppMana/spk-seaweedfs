// OCI image sourcing for the weed binary. When volume.yaml carries a
// weed.image reference, the bootstrap pulls that image (anonymous,
// linux/amd64), extracts the configured binary paths from its layers,
// and caches them by manifest digest under weed.cacheDir. The resolved
// weed path is written to --weed-bin-out for service_prestart to exec
// instead of the binary packaged in the SPK.
//
// Cache layout:
//
//	<cacheDir>/<algo>-<hex>/<basename>   extracted binaries, 0755
//	<cacheDir>/<algo>-<hex>/.complete    written last; marks a usable entry
//	<cacheDir>/current                   symlink to the digest dir
//
// A cache entry is reused without any network I/O. When the reference
// is a mutable tag, resolving it requires the registry; if that fails
// and a previous `current` entry exists, the bootstrap falls back to it
// so an offline NAS can still restart the volume daemon.
package main

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

const defaultWeedBinaryPath = "/usr/bin/weed"

// materializeWeed resolves weed.image into extracted binaries on disk
// and returns the local path of the weed binary. Returns "" when no
// image is configured (packaged binary is used).
func materializeWeed(ctx context.Context, c *config) (string, error) {
	if c.Weed.Image == "" {
		return "", nil
	}
	binaries := c.Weed.Binaries
	if len(binaries) == 0 {
		binaries = []string{defaultWeedBinaryPath}
	}
	cacheDir := c.Weed.CacheDir
	if cacheDir == "" {
		if v := os.Getenv("SYNOPKG_PKGVAR"); v != "" {
			cacheDir = filepath.Join(v, "oci")
		} else {
			return "", errors.New("weed.cacheDir is required (SYNOPKG_PKGVAR not set)")
		}
	}

	var opts []name.Option
	if c.Weed.PlainHTTP {
		opts = append(opts, name.Insecure)
	}
	ref, err := name.ParseReference(c.Weed.Image, opts...)
	if err != nil {
		return "", fmt.Errorf("parse weed.image: %w", err)
	}

	remoteOpts := []remote.Option{
		remote.WithContext(ctx),
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
		remote.WithPlatform(v1.Platform{OS: "linux", Architecture: "amd64"}),
	}

	digest, err := resolveDigest(ref, c.Weed.Digest, remoteOpts)
	if err != nil {
		// Tag resolution needs the registry; fall back to the last
		// successfully extracted entry so offline restarts keep working.
		if p, ferr := cachedCurrent(cacheDir, binaries); ferr == nil {
			fmt.Fprintf(os.Stderr, "synology-volume-bootstrap: registry unreachable (%v); using cached %s\n", err, p)
			return p, nil
		}
		return "", fmt.Errorf("resolve weed.image digest: %w", err)
	}

	entryDir := filepath.Join(cacheDir, strings.Replace(digest.String(), ":", "-", 1))
	if p, err := cachedEntry(entryDir, binaries); err == nil {
		if err := setCurrent(cacheDir, entryDir); err != nil {
			return "", err
		}
		return p, nil
	}

	img, err := remote.Image(ref, remoteOpts...)
	if err != nil {
		return "", fmt.Errorf("pull %s: %w", ref, err)
	}
	if got, err := img.Digest(); err != nil {
		return "", err
	} else if got != digest {
		return "", fmt.Errorf("weed.image digest mismatch: manifest %s, expected %s", got, digest)
	}

	extracted, err := extractFromImage(img, binaries, entryDir)
	if err != nil {
		return "", fmt.Errorf("extract from %s: %w", ref, err)
	}
	if err := os.WriteFile(filepath.Join(entryDir, ".complete"), nil, 0o644); err != nil {
		return "", err
	}
	if err := setCurrent(cacheDir, entryDir); err != nil {
		return "", err
	}
	return weedPath(extracted), nil
}

// resolveDigest returns the manifest digest for ref. A configured
// weed.digest pin wins without touching the network; otherwise the
// registry is consulted (HEAD).
func resolveDigest(ref name.Reference, pin string, remoteOpts []remote.Option) (v1.Hash, error) {
	if d, ok := ref.(name.Digest); ok {
		h, err := v1.NewHash(d.DigestStr())
		if err != nil {
			return v1.Hash{}, err
		}
		if pin != "" && pin != h.String() {
			return v1.Hash{}, fmt.Errorf("weed.digest %q contradicts digest reference %q", pin, h)
		}
		return h, nil
	}
	if pin != "" {
		return v1.NewHash(pin)
	}
	desc, err := remote.Head(ref, remoteOpts...)
	if err != nil {
		return v1.Hash{}, err
	}
	return desc.Digest, nil
}

// cachedEntry returns the weed path inside a complete cache entry.
func cachedEntry(entryDir string, binaries []string) (string, error) {
	if _, err := os.Stat(filepath.Join(entryDir, ".complete")); err != nil {
		return "", err
	}
	var paths []string
	for _, b := range binaries {
		p := filepath.Join(entryDir, path.Base(b))
		if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
			return "", fmt.Errorf("cache entry %s incomplete: missing %s", entryDir, path.Base(b))
		}
		paths = append(paths, p)
	}
	return weedPath(paths), nil
}

func cachedCurrent(cacheDir string, binaries []string) (string, error) {
	target, err := os.Readlink(filepath.Join(cacheDir, "current"))
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(cacheDir, target)
	}
	return cachedEntry(target, binaries)
}

func setCurrent(cacheDir, entryDir string) error {
	link := filepath.Join(cacheDir, "current")
	tmp := link + ".tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(filepath.Base(entryDir), tmp); err != nil {
		return err
	}
	return os.Rename(tmp, link)
}

// weedPath picks the binary to exec: the one named "weed", else the
// first extracted path.
func weedPath(paths []string) string {
	for _, p := range paths {
		if filepath.Base(p) == "weed" {
			return p
		}
	}
	if len(paths) > 0 {
		return paths[0]
	}
	return ""
}

// extractFromImage walks the image layers top-down and extracts the
// requested absolute paths into entryDir (flattened to basenames).
// Standard OCI whiteouts are honored: a .wh.<name> entry or an opaque
// directory in an upper layer hides the path in lower layers.
func extractFromImage(img v1.Image, wanted []string, entryDir string) ([]string, error) {
	if err := os.MkdirAll(entryDir, 0o755); err != nil {
		return nil, err
	}
	layers, err := img.Layers()
	if err != nil {
		return nil, err
	}

	want := map[string]string{} // normalized layer path -> basename
	for _, w := range wanted {
		want[strings.TrimPrefix(path.Clean(w), "/")] = path.Base(w)
	}
	found := map[string]string{}   // normalized path -> extracted file
	deleted := map[string]bool{}   // whiteout'd exact paths
	opaque := map[string]bool{}    // opaque dirs (hide lower-layer contents)

	hidden := func(p string) bool {
		if deleted[p] {
			return true
		}
		for d := path.Dir(p); d != "."; d = path.Dir(d) {
			if deleted[d] || opaque[d] {
				return true
			}
		}
		return false
	}

	for i := len(layers) - 1; i >= 0 && len(found) < len(want); i-- {
		rc, err := layers[i].Uncompressed()
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(rc)
		layerDeleted := map[string]bool{}
		layerOpaque := map[string]bool{}
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				rc.Close()
				return nil, err
			}
			p := strings.TrimPrefix(path.Clean(hdr.Name), "/")
			base := path.Base(p)
			dir := path.Dir(p)
			if base == ".wh..wh..opq" {
				layerOpaque[dir] = true
				continue
			}
			if strings.HasPrefix(base, ".wh.") {
				layerDeleted[path.Join(dir, strings.TrimPrefix(base, ".wh."))] = true
				continue
			}
			out, ok := want[p]
			if !ok || found[p] != "" || hidden(p) {
				continue
			}
			if hdr.Typeflag != tar.TypeReg {
				rc.Close()
				return nil, fmt.Errorf("layer entry /%s is not a regular file (type %c); symlinked binaries are not supported", p, hdr.Typeflag)
			}
			dst := filepath.Join(entryDir, out)
			if err := writeExtracted(dst, tr); err != nil {
				rc.Close()
				return nil, err
			}
			found[p] = dst
		}
		rc.Close()
		// Whiteouts in this layer only affect layers below it.
		for p := range layerDeleted {
			deleted[p] = true
		}
		for p := range layerOpaque {
			opaque[p] = true
		}
	}

	var missing []string
	var paths []string
	for p, out := range want {
		if f := found[p]; f != "" {
			_ = out
			paths = append(paths, f)
		} else {
			missing = append(missing, "/"+p)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("paths not found in image: %s", strings.Join(missing, ", "))
	}
	return paths, nil
}

func writeExtracted(dst string, r io.Reader) error {
	tmp := dst + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
