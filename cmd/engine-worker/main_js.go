//go:build js && wasm

// Command engine-worker is the demo page's engine (GOOS=js), run in a
// dedicated Web Worker (engine-worker.js): the daemon with the browser
// executor (one worker per container) and an in-memory image store whose
// registry is the page's own same-origin /v2 mirror, its API served to the
// page through the bridge. It is the reference for what the playground's
// engine worker adds to serve the dashboard (see the README).
//
// Messages to the page: {t:"ready", version}, {t:"fatal", message}, and
// the bridge's http.* messages.
package main

import (
	"strings"
	"syscall/js"
	"time"

	"github.com/go-wasmcontainers/engine/api/server"
	"github.com/go-wasmcontainers/engine/container"
	"github.com/go-wasmcontainers/engine/daemon"
	"github.com/go-wasmcontainers/engine/image"
	"github.com/go-wasmcontainers/engine/rootfs"
	"github.com/go-wasmcontainers/engine/runtime/browser"

	"github.com/go-wasmcontainers/dashboard/bridge"
)

var version = "dev"

func post(v map[string]any) { js.Global().Call("postMessage", js.ValueOf(v)) }

func main() {
	href := js.Global().Get("location").Get("href").String()
	base := href[:strings.LastIndexByte(href, '/')+1]
	v := js.Global().Get("location").Get("search").String()

	exec := browser.New(browser.Options{WorkerURL: base + "container-worker.js" + v, DefaultMemoryLimitBytes: 128 << 20})
	lim := image.DefaultLimits
	lim.MaxImageBytes = 128 << 20
	lim.MaxBlobBytes = 64 << 20
	d, err := daemon.New(daemon.Options{
		Backend:  image.NewMemoryBackend(256 << 20),
		Limits:   lim,
		Unpack:   rootfs.Limits{MaxBytes: 256 << 20, MaxFiles: 1 << 15, MaxRatio: 200, MaxPathLen: 4096, MaxDepth: 128},
		Registry: image.RegistryOptions{Mirrors: map[string]string{"docker.io": strings.TrimSuffix(base, "/")}},
		Containers: container.Options{
			Executor: exec, MemoryLimitBytes: 128 << 20, Timeout: 10 * time.Minute,
			Quota: rootfs.Quota{MaxBytes: 32 << 20, MaxFiles: 1 << 13}, MaxLogBytes: 2 << 20, MaxContainers: 64,
		},
		MaxLowers: 8,
	})
	if err != nil {
		post(map[string]any{"t": "fatal", "message": err.Error()})
		return
	}
	_, handle := bridge.ServeJS(server.New(d, server.Info{Version: version}), js.Global())
	js.Global().Set("onmessage", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) > 0 {
			handle(a[0].Get("data"))
		}
		return nil
	}))
	post(map[string]any{"t": "ready", "version": version})
	select {}
}
