package images

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-wasmcontainers/engine/image"
)

// Pinned is an image fetched from a registry by its manifest digest, so the
// site and the tests hold exactly the bytes that were reviewed.
type Pinned struct {
	// Registry is the base URL ("https://ghcr.io"), Repo the repository
	// there, Tag the tag it is published under, Digest the manifest's.
	Registry, Repo, Tag, Digest string
	// Names are the repositories it is served under in the static mirror
	// ("library/shell", so that "shell:0.1.0" finds it; and its own path,
	// for a mirror of its registry).
	Names []string
}

// Shell is go-wasmcontainers/shell 0.1.0: /bin/sh and a base userland, the
// image `wctr exec -it CONTAINER sh` needs.
var Shell = Pinned{
	Registry: "https://ghcr.io", Repo: "go-wasmcontainers/shell", Tag: "0.1.0",
	Digest: "sha256:79b185bfd505b523227396208976641e616723c3238c2a2edf18adcf15423b0e",
	Names:  []string{"library/shell", "go-wasmcontainers/shell"},
}

// Bounds on what Fetch accepts from a registry.
const (
	maxManifest = 1 << 20
	maxBlob     = 64 << 20
	maxBlobs    = 32
)

// Fetch returns the static mirror files of p: its manifest under its tag,
// "latest" and its digest, and its blobs, for each of p.Names. Every byte
// is checked against its digest. cache, when not "", keeps the verified
// blobs by digest across runs (they are checked again when read).
func Fetch(ctx context.Context, hc *http.Client, p Pinned, cache string) (map[string][]byte, error) {
	token, err := pullToken(ctx, hc, p)
	if err != nil {
		return nil, err
	}
	get := func(kind, digest string, max int64) ([]byte, error) {
		if b, ok := cached(cache, digest); ok {
			return b, nil
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.Registry+"/v2/"+p.Repo+"/"+kind+"/"+digest, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", image.MediaTypeOCIManifest)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := hc.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s %s: %s", kind, digest, resp.Status)
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
		if err != nil {
			return nil, err
		}
		if int64(len(b)) > max {
			return nil, fmt.Errorf("%s %s: over %d bytes", kind, digest, max)
		}
		if got := "sha256:" + hexSum(b); got != digest {
			return nil, fmt.Errorf("%s %s: the registry sent %s", kind, digest, got)
		}
		store(cache, digest, b)
		return b, nil
	}
	mb, err := get("manifests", p.Digest, maxManifest)
	if err != nil {
		return nil, err
	}
	var m image.Manifest
	if err := json.Unmarshal(mb, &m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	descs := append([]image.Descriptor{m.Config}, m.Layers...)
	if len(descs) > maxBlobs {
		return nil, fmt.Errorf("manifest: %d blobs", len(descs))
	}
	files := map[string][]byte{}
	for _, d := range descs {
		digest := string(d.Digest)
		if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 || strings.ContainsAny(digest, "/.") {
			return nil, fmt.Errorf("manifest: blob digest %q", digest)
		}
		b, err := get("blobs", digest, maxBlob)
		if err != nil {
			return nil, err
		}
		if int64(len(b)) != d.Size {
			return nil, fmt.Errorf("blob %s: %d bytes, the manifest says %d", digest, len(b), d.Size)
		}
		for _, n := range p.Names {
			files["v2/"+n+"/blobs/"+digest] = b
		}
	}
	for _, n := range p.Names {
		for _, ref := range []string{p.Tag, "latest", p.Digest} {
			files["v2/"+n+"/manifests/"+ref] = mb
		}
	}
	return files, nil
}

// pullToken asks the registry for an anonymous pull token (ghcr.io and the
// Docker registry protocol); a registry that wants none answers 404.
func pullToken(ctx context.Context, hc *http.Client, p Pinned) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.Registry+"/token?scope=repository:"+p.Repo+":pull", nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	var t struct{ Token string }
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&t) != nil {
		return "", fmt.Errorf("pull token for %s: %s", p.Repo, resp.Status)
	}
	return t.Token, nil
}

func hexSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// cached reads a verified blob from the cache.
func cached(dir, digest string) ([]byte, bool) {
	if dir == "" {
		return nil, false
	}
	b, err := os.ReadFile(filepath.Join(dir, strings.TrimPrefix(digest, "sha256:")))
	if err != nil || "sha256:"+hexSum(b) != digest {
		return nil, false
	}
	return b, true
}

// store keeps a verified blob in the cache; a cache that cannot be written
// only costs the next run a download.
func store(dir, digest string, b []byte) {
	if dir == "" || os.MkdirAll(dir, 0o755) != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, strings.TrimPrefix(digest, "sha256:")), b, 0o644)
}

// CacheDir is where Fetch keeps blobs on this machine ("" when there is no
// user cache directory).
func CacheDir() string {
	d, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "go-wasmcontainers-dashboard", "blobs")
}
