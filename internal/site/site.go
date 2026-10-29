// Package site builds the dashboard's standalone demo page: the page and
// its scripts, the dashboard and the engine compiled for js/wasm, the
// container worker, and a same-origin static registry (/v2) holding the
// demo images. It is what GitHub Pages serves, and what the browser tests
// load.
package site

import (
	"context"
	"embed"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-wasmcontainers/engine/runtime/browser"

	"github.com/go-wasmcontainers/dashboard/internal/images"
)

//go:embed web
var web embed.FS

// Options configure a build.
type Options struct {
	// Root is the module's root directory; Out the directory to write.
	Root, Out string
	// Version is stamped into the programs and every asset URL.
	Version string
	// Log receives progress.
	Log io.Writer
	// Go is the go command (default "go").
	Go string
}

func (o *Options) gocmd() string {
	if o.Go == "" {
		return "go"
	}
	return o.Go
}

// Build writes the site and returns the files it wrote, relative to Out.
func Build(ctx context.Context, o Options) ([]string, error) {
	if o.Log == nil {
		o.Log = io.Discard
	}
	if strings.ContainsAny(o.Version, "\"'<>&\\ ") {
		return nil, fmt.Errorf("version %q: only plain characters", o.Version)
	}
	if err := os.MkdirAll(o.Out, 0o755); err != nil {
		return nil, err
	}
	var wrote []string
	put := func(name string, b []byte) error {
		p := filepath.Join(o.Out, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		wrote = append(wrote, name)
		return os.WriteFile(p, b, 0o644)
	}
	// The page and its scripts.
	entries, err := web.ReadDir("web")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		b, err := web.ReadFile("web/" + e.Name())
		if err != nil {
			return nil, err
		}
		if err := put(e.Name(), []byte(strings.ReplaceAll(string(b), "{{BUILD}}", o.Version))); err != nil {
			return nil, err
		}
	}
	if err := put("container-worker.js", browser.WorkerJS); err != nil {
		return nil, err
	}
	if err := put(".nojekyll", nil); err != nil {
		return nil, err
	}
	// wasm_exec.js from the toolchain that builds the programs.
	root, err := exec.CommandContext(ctx, o.gocmd(), "env", "GOROOT").Output()
	if err != nil {
		return nil, fmt.Errorf("go env GOROOT: %w", err)
	}
	glue, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(root)), "lib", "wasm", "wasm_exec.js"))
	if err != nil {
		return nil, err
	}
	if err := put("wasm_exec.js", glue); err != nil {
		return nil, err
	}
	// The programs.
	for _, p := range []struct{ pkg, out string }{{"./cmd/dashboard", "dashboard.wasm"}, {"./cmd/engine-worker", "engine.wasm"}} {
		fmt.Fprintf(o.Log, "building %s\n", p.out)
		cmd := exec.CommandContext(ctx, o.gocmd(), "build", "-trimpath", "-ldflags=-s -w -X main.version="+o.Version,
			"-o", filepath.Join(o.Out, p.out), p.pkg)
		cmd.Dir = o.Root
		cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm", "CGO_ENABLED=0", "GOWORK=off")
		if msg, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("building %s: %v\n%s", p.pkg, err, msg)
		}
		wrote = append(wrote, p.out)
	}
	// The demo images and their registry.
	mods := map[string][]byte{}
	for _, p := range images.Programs {
		fmt.Fprintf(o.Log, "building image %s\n", p.Name)
		b, err := images.Compile(o.Root, p.Name)
		if err != nil {
			return nil, err
		}
		mods[p.Name] = b
	}
	files := images.Registry(mods)
	// The shell image, mirrored by digest (`wctr run -d shell:0.1.0`, then
	// Open shell): the page reaches nothing but its own origin.
	fmt.Fprintf(o.Log, "mirroring %s@%s\n", images.Shell.Repo, images.Shell.Digest)
	shell, err := images.Fetch(ctx, &http.Client{Timeout: 5 * time.Minute}, images.Shell, images.CacheDir())
	if err != nil {
		return nil, fmt.Errorf("mirroring %s: %w", images.Shell.Repo, err)
	}
	for n, b := range shell {
		files[n] = b
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := put(n, files[n]); err != nil {
			return nil, err
		}
	}
	sort.Strings(wrote)
	return wrote, nil
}

// Handler serves a built site at prefix, with the isolation headers when
// isolate is set (without them the page isolates itself with its service
// worker, as on GitHub Pages).
func Handler(dir, prefix string, isolate bool) http.Handler {
	fs := http.StripPrefix(prefix, http.FileServer(http.Dir(dir)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isolate {
			w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
			w.Header().Set("Cross-Origin-Embedder-Policy", "require-corp")
		}
		if strings.HasSuffix(r.URL.Path, ".wasm") {
			w.Header().Set("Content-Type", "application/wasm")
		}
		fs.ServeHTTP(w, r)
	})
}
