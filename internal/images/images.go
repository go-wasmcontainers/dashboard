// Package images builds the demo images and serves them the way a
// registry does, as static files: the demo page's same-origin /v2 mirror
// and the tests' in-process registry are the same bytes.
//
// Each image is a CNCF wasm artifact (one application/wasm layer, the
// application/vnd.wasm.config.v0+json config), named
// library/<name>:latest so the familiar name "<name>" resolves to it
// through the engine's registry mirror.
package images

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/go-wasmcontainers/engine/image"
)

// Programs are the demo images: the directory under images/ in this
// repository, and what they do.
var Programs = []struct{ Name, Description string }{
	{"hello", "prints a greeting and exits"},
	{"clock", "prints a coloured line every second (logs to follow)"},
	{"fail", "writes to stderr and exits with status 3"},
}

// Compile builds images/<name> of the module rooted at root for wasip1.
func Compile(root, name string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "dashboard-image-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, name+".wasm")
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", out, "./images/"+name)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0", "GOWORK=off")
	if msg, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("building %s for wasip1: %v\n%s", name, err, msg)
	}
	return os.ReadFile(out)
}

// Registry returns the files of a static registry holding modules (name ->
// wasm bytes), keyed by their path under the mirror's base URL.
func Registry(modules map[string][]byte) map[string][]byte {
	files := map[string][]byte{}
	names := make([]string, 0, len(modules))
	for n := range modules {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		repo := "v2/library/" + n + "/"
		blob := func(b []byte) image.Descriptor {
			d := image.FromBytes(b)
			files[repo+"blobs/"+string(d)] = b
			return image.Descriptor{Digest: d, Size: int64(len(b))}
		}
		layer := blob(modules[n])
		layer.MediaType = image.MediaTypeWasmLayer
		layer.Annotations = map[string]string{image.AnnotationTitle: n + ".wasm"}
		cfg := blob(mustJSON(map[string]any{
			"created": "2026-10-26T00:00:00Z", "architecture": "wasm", "os": "wasip1",
			"layerDigests": []string{string(layer.Digest)},
		}))
		cfg.MediaType = image.MediaTypeWasmConfig
		m := image.Manifest{SchemaVersion: 2, MediaType: image.MediaTypeOCIManifest, Config: cfg, Layers: []image.Descriptor{layer}}
		mb := mustJSON(m)
		md := image.FromBytes(mb)
		files[repo+"manifests/latest"] = mb
		files[repo+"manifests/"+string(md)] = mb
	}
	return files
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // only plain maps and structs are marshalled
	}
	return b
}
