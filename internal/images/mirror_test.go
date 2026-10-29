package images

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-wasmcontainers/engine/image"
)

// registry is a fake registry serving one image; tamper changes what it
// sends for a path.
type registry struct {
	files  map[string][]byte
	tamper map[string][]byte
	token  int // the status of /token
	hits   int
}

func (r *registry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.hits++
	if req.URL.Path == "/token" {
		if r.token != 200 {
			w.WriteHeader(r.token)
			return
		}
		w.Write([]byte(`{"token":"t0k"}`))
		return
	}
	if req.Header.Get("Authorization") != "Bearer t0k" && r.token == 200 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	p := strings.TrimPrefix(req.URL.Path, "/")
	if b, ok := r.tamper[p]; ok {
		w.Write(b)
		return
	}
	b, ok := r.files[p]
	if !ok {
		http.NotFound(w, req)
		return
	}
	w.Write(b)
}

func image1(t *testing.T) (*registry, Pinned) {
	t.Helper()
	cfg, layer := []byte(`{"architecture":"wasm","os":"wasip1"}`), []byte("a tar.gz layer")
	d := func(b []byte) image.Descriptor { return image.Descriptor{Digest: image.FromBytes(b), Size: int64(len(b))} }
	m := image.Manifest{SchemaVersion: 2, MediaType: image.MediaTypeOCIManifest, Config: d(cfg), Layers: []image.Descriptor{d(layer)}}
	mb, _ := json.Marshal(m)
	md := string(image.FromBytes(mb))
	r := &registry{token: 200, tamper: map[string][]byte{}, files: map[string][]byte{
		"v2/o/shell/manifests/" + md:                    mb,
		"v2/o/shell/blobs/" + string(m.Config.Digest):    cfg,
		"v2/o/shell/blobs/" + string(m.Layers[0].Digest): layer,
	}}
	return r, Pinned{Repo: "o/shell", Tag: "0.1.0", Digest: md, Names: []string{"library/shell", "o/shell"}}
}

func TestFetch(t *testing.T) {
	r, p := image1(t)
	srv := httptest.NewServer(r)
	defer srv.Close()
	p.Registry = srv.URL
	ctx := context.Background()
	cache := t.TempDir()

	files, err := Fetch(ctx, srv.Client(), p, cache)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range p.Names {
		for _, ref := range []string{"0.1.0", "latest", p.Digest} {
			if _, ok := files["v2/"+n+"/manifests/"+ref]; !ok {
				t.Errorf("no manifest %s:%s", n, ref)
			}
		}
	}
	if len(files) != 2*3+2*2 {
		t.Errorf("%d files", len(files))
	}
	// A second fetch reads the verified cache: only the token is asked.
	r.hits = 0
	if _, err := Fetch(ctx, srv.Client(), p, cache); err != nil || r.hits != 1 {
		t.Errorf("cached fetch: %v, %d requests", err, r.hits)
	}
	// A corrupted cache entry is not believed.
	ents, _ := os.ReadDir(cache)
	os.WriteFile(filepath.Join(cache, ents[0].Name()), []byte("corrupt"), 0o644)
	r.hits = 0
	if _, err := Fetch(ctx, srv.Client(), p, cache); err != nil || r.hits != 2 {
		t.Errorf("corrupt cache: %v, %d requests", err, r.hits)
	}

	// Tampering, at every place it can happen.
	for _, c := range []struct {
		path string
		body []byte
		want string
	}{
		{"v2/o/shell/manifests/" + p.Digest, []byte(`{"schemaVersion":2}`), "the registry sent"},
		{"v2/o/shell/blobs/" + string(image.FromBytes([]byte("a tar.gz layer"))), []byte("another layer"), "the registry sent"},
	} {
		r.tamper = map[string][]byte{c.path: c.body}
		if _, err := Fetch(ctx, srv.Client(), p, ""); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("tampered %s: %v", c.path, err)
		}
	}
	r.tamper = map[string][]byte{}

	// A manifest that lies about sizes, names a path, is not JSON, or
	// names too many blobs; and a registry that refuses.
	bad := func(m any) Pinned {
		mb, _ := json.Marshal(m)
		q := p
		q.Digest = string(image.FromBytes(mb))
		r.files["v2/o/shell/manifests/"+q.Digest] = mb
		return q
	}
	layer := image.Descriptor{Digest: image.FromBytes([]byte("a tar.gz layer")), Size: 3}
	lie := bad(image.Manifest{SchemaVersion: 2, Config: layer, Layers: nil})
	path := bad(image.Manifest{SchemaVersion: 2, Config: image.Descriptor{Digest: image.Digest("sha256:../../../etc/passwd" + strings.Repeat("0", 43)), Size: 1}})
	many := bad(image.Manifest{SchemaVersion: 2, Layers: make([]image.Descriptor, maxBlobs)})
	notJSON := p
	notJSON.Digest = string(image.FromBytes([]byte("nope")))
	r.files["v2/o/shell/manifests/"+notJSON.Digest] = []byte("nope")
	missing := p
	missing.Digest = "sha256:" + strings.Repeat("a", 64)
	for name, c := range map[string]struct {
		p    Pinned
		want string
	}{
		"size": {lie, "the manifest says"}, "path": {path, "blob digest"}, "many": {many, "blobs"},
		"json": {notJSON, "manifest:"}, "missing": {missing, "404"},
	} {
		if _, err := Fetch(ctx, srv.Client(), c.p, ""); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Too large.
	big := p
	big.Digest = "sha256:" + strings.Repeat("b", 64)
	r.files["v2/o/shell/manifests/"+big.Digest] = make([]byte, maxManifest+1)
	if _, err := Fetch(ctx, srv.Client(), big, ""); err == nil || !strings.Contains(err.Error(), "over") {
		t.Errorf("too large: %v", err)
	}

	// Tokens: a registry without them; one that refuses; none reachable.
	r.token = 404
	if _, err := Fetch(ctx, srv.Client(), p, ""); err != nil {
		t.Errorf("no token service: %v", err)
	}
	r.token = 500
	if _, err := Fetch(ctx, srv.Client(), p, ""); err == nil || !strings.Contains(err.Error(), "pull token") {
		t.Errorf("token refused: %v", err)
	}
	dead := p
	dead.Registry = "http://127.0.0.1:1"
	if _, err := Fetch(ctx, srv.Client(), dead, ""); err == nil {
		t.Error("an unreachable registry answered")
	}
	badURL := p
	badURL.Registry = "http://bad host"
	if _, err := Fetch(ctx, srv.Client(), badURL, ""); err == nil {
		t.Error("a bad URL was fetched")
	}
	if CacheDir() == "" {
		t.Log("no user cache directory here")
	}
}
