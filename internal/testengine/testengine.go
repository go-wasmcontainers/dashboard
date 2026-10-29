// Package testengine runs a real go-wasmcontainers engine in the test's
// process: the daemon with the wazero executor and an in-memory image
// store, its API handler, and a registry serving the demo images (built
// for wasip1 at test time). The dashboard reaches it through the same
// bridge the browser uses.
package testengine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/go-wasmcontainers/engine/api/server"
	"github.com/go-wasmcontainers/engine/container"
	"github.com/go-wasmcontainers/engine/daemon"
	"github.com/go-wasmcontainers/engine/image"
	"github.com/go-wasmcontainers/engine/rootfs"
	"github.com/go-wasmcontainers/engine/runtime/wazero"
	"github.com/go-wasmcontainers/engine/wasi"

	"github.com/go-wasmcontainers/dashboard/bridge"
	"github.com/go-wasmcontainers/dashboard/internal/images"
)

var (
	once    sync.Once
	modules map[string][]byte
	errMods error
)

// Root is this module's root directory.
func Root() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// Modules compiles the demo images once per process.
func Modules(tb testing.TB) map[string][]byte {
	tb.Helper()
	once.Do(func() {
		modules = map[string][]byte{}
		for _, p := range images.Programs {
			b, err := images.Compile(Root(), p.Name)
			if err != nil {
				errMods = err
				return
			}
			modules[p.Name] = b
		}
	})
	if errMods != nil {
		tb.Fatal(errMods)
	}
	return modules
}

var (
	shellOnce  sync.Once
	shellCache map[string][]byte
	errShell   error
)

// shellFiles mirrors the shell image once per process (from the user cache
// when it holds the verified blobs; else from ghcr.io). A test that needs
// the shell skips when it cannot be had (no network) -- see HasShell.
func shellFiles(tb testing.TB) map[string][]byte {
	shellOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		shellCache, errShell = images.Fetch(ctx, &http.Client{Timeout: 2 * time.Minute}, images.Shell, images.CacheDir())
	})
	return shellCache
}

// HasShell skips tb unless the shell image could be mirrored.
func HasShell(tb testing.TB) {
	tb.Helper()
	shellFiles(tb)
	if errShell != nil {
		tb.Skipf("no shell image: %v", errShell)
	}
}

// Engine is a running engine.
type Engine struct {
	// Handler is the engine's API.
	Handler http.Handler
	// Client reaches Handler through the bridge, as the page does.
	Client *http.Client
	// Bridge is the server end of the bridge.
	Bridge *bridge.Server
}

// New starts an engine whose registry mirror serves the demo images under
// their familiar names (hello, clock, fail), and hello's module again under
// each of the extra names.
func New(tb testing.TB, extra ...string) *Engine {
	tb.Helper()
	mods := map[string][]byte{}
	for n, b := range Modules(tb) {
		mods[n] = b
	}
	for _, n := range extra {
		mods[n] = mods["hello"]
	}
	files := images.Registry(mods)
	for n, b := range shellFiles(tb) {
		files[n] = b
	}
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[r.URL.Path[1:]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	tb.Cleanup(reg.Close)
	// The engine's own WASI with the WASIX process calls: what exec and the
	// shell image's pipelines need (as `wctrd --wasi engine` and the browser).
	exec := wazero.New(wazero.Options{EngineWASI: true, HostModules: []wasi.HostModule{wasi.WASIX}})
	d, err := daemon.New(daemon.Options{
		Backend:  image.NewMemoryBackend(256 << 20),
		Limits:   image.DefaultLimits,
		Unpack:   rootfs.Limits{MaxBytes: 64 << 20, MaxFiles: 1 << 12, MaxRatio: 200, MaxPathLen: 4096, MaxDepth: 64},
		Registry: image.RegistryOptions{Mirrors: map[string]string{"docker.io": reg.URL, "ghcr.io": reg.URL}},
		Containers: container.Options{
			Executor: exec, MemoryLimitBytes: 64 << 20, Timeout: time.Minute,
			Quota: rootfs.Quota{MaxBytes: 16 << 20, MaxFiles: 1 << 10}, MaxLogBytes: 1 << 20, MaxContainers: 300,
		},
		MaxLowers: 8,
	})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		d.Close(ctx)
		exec.Close(ctx)
	})
	h := server.New(d, server.Info{Version: "test"})
	var srv *bridge.Server
	tr := bridge.NewTransport(func(m bridge.Message) { srv.Deliver(m) })
	srv = bridge.NewServer(h, tr.Deliver)
	tb.Cleanup(srv.Close)
	return &Engine{Handler: h, Client: &http.Client{Transport: tr}, Bridge: srv}
}
