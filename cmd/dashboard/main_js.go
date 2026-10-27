//go:build js && wasm

// Command dashboard is the dashboard page's program (GOOS=js): it reaches
// the engine running in the Web Worker the page created (window.__wctrEngine)
// through the bridge, and draws the go-widgets scene on the #screen canvas.
// The terminal view runs wctr in this program, over the same bridge.
//
// For tests the page gets window.__dashboard.state(): the scene's Probe as
// JSON (view, control boxes in canvas pixels, rows, terminal text, ...).
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"syscall/js"

	"github.com/go-wasmcontainers/engine/api"
	"github.com/go-wasmcontainers/engine/cli"
	"github.com/go-wasmcontainers/engine/cli/lineedit"
	"github.com/go-widgets/mvvm"
	"github.com/go-widgets/webcanvas"
	"github.com/spf13/cobra"

	"github.com/go-wasmcontainers/dashboard/bridge"
	"github.com/go-wasmcontainers/dashboard/engine"
	"github.com/go-wasmcontainers/dashboard/ui"
	"github.com/go-wasmcontainers/dashboard/vm"
)

var version = "dev"

func main() {
	g := js.Global()
	worker := g.Get("__wctrEngine")
	if worker.IsUndefined() || worker.IsNull() {
		println("dashboard: the page has no engine worker (window.__wctrEngine)")
		return
	}
	hc := &http.Client{Transport: bridge.DialJS(worker)}
	opts := cli.Options{Version: version, DefaultHost: "inproc://engine",
		Client: func(string) (*api.Client, error) { return api.NewClientWith(hc, "http://engine"), nil }}

	var scene *ui.Scene
	q := mvvm.NewQueue(func() {
		if scene != nil {
			scene.Wake()
		}
	})
	app := vm.NewApp(engine.New(hc, "http://engine"), vm.NewCLI(cli.RootCmd(opts)), q.Post)
	app.Terminal.SetShell(vm.WctrShell{Root: func() *cobra.Command { return cli.RootCmd(opts) }},
		lineedit.WctrCompleter(opts, vm.Builtins))
	w, h := g.Get("innerWidth").Int(), g.Get("innerHeight").Int()
	scene = ui.New(app, q, w, h)
	connectClass(g, worker, app, q)

	g.Set("__dashboard", map[string]any{
		"version": version,
		"state": js.FuncOf(func(js.Value, []js.Value) any {
			b, _ := json.Marshal(scene.Probe())
			return string(b)
		}),
	})
	go app.RunLive(context.Background(), vm.PollInterval)
	webcanvas.Run("screen", scene)
}
