//go:build js && wasm

// Package host is the dashboard's side of the page it runs in (GOOS=js):
// what connects the viewmodels to the page and to its engine worker beyond
// the engine's API -- joining a class, and the GPU surfaces (their frames
// and their input). Any page's own command can call it (the dashboard's
// cmd/dashboard, the playground's), so the wiring exists once.
package host

import (
	"errors"
	"syscall/js"

	"github.com/go-widgets/mvvm"

	"github.com/go-wasmcontainers/dashboard/vm"
)

// classHost joins a class through the page and its engine worker. The page
// owns the relay (it starts the worker with it and lets the worker's CSP
// reach exactly that origin) and says so with window.__wctrClass:
//
//	{ relay: "https://host[:port]" | "",
//	  useRelay(origin): remember origin ("" forgets) and restart the engine,
//	  status(): the last {t:"class"} message, or null }
//
// The join code goes from the dialog to the worker once ({t:"join", code,
// name}), as the worker of go-wasmcontainers/playground expects; the
// worker answers with {t:"class", state, relay, label, domain, network,
// subnet, error}. A page without window.__wctrClass cannot join a class and
// the dashboard does not offer it.
type classHost struct {
	page, worker js.Value
}

func (h classHost) Relay() string {
	if r := h.page.Get("relay"); r.Type() == js.TypeString {
		return r.String()
	}
	return ""
}

func (h classHost) UseRelay(origin string) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("the page could not restart the engine with that relay")
		}
	}()
	h.page.Call("useRelay", origin)
	return nil
}

func (h classHost) Join(code, name string) error {
	h.worker.Call("postMessage", map[string]any{"t": "join", "code": code, "name": name})
	return nil
}

func (h classHost) Leave() error {
	h.worker.Call("postMessage", map[string]any{"t": "leave"})
	return nil
}

// str reads a string field of a message ("" otherwise); vm.Class.Report
// cleans and bounds it.
func str(v js.Value, name string) string {
	if f := v.Get(name); f.Type() == js.TypeString {
		return f.String()
	}
	return ""
}

func classStatus(m js.Value) vm.ClassStatus {
	return vm.ClassStatus{State: str(m, "state"), Relay: str(m, "relay"), Label: str(m, "label"), Domain: str(m, "domain"),
		Network: str(m, "network"), Subnet: str(m, "subnet"), Error: str(m, "error")}
}

// ConnectClass offers the class dialog when the page supports it
// (g.__wctrClass), and follows the worker's class reports.
func ConnectClass(g, worker js.Value, app *vm.App, q *mvvm.Queue) {
	page := g.Get("__wctrClass")
	if page.Type() != js.TypeObject || page.Get("useRelay").Type() != js.TypeFunction {
		return
	}
	h := classHost{page: page, worker: worker}
	q.Post(func() { app.Class.SetHost(h) })
	worker.Call("addEventListener", "message", js.FuncOf(func(_ js.Value, a []js.Value) any {
		m := a[0].Get("data")
		if m.Type() == js.TypeObject && str(m, "t") == "class" {
			st := classStatus(m)
			q.Post(func() { app.Class.Report(st) })
		}
		return nil
	}))
	if f := page.Get("status"); f.Type() == js.TypeFunction {
		if m := f.Invoke(); m.Type() == js.TypeObject && str(m, "state") != "" {
			st := classStatus(m)
			q.Post(func() { app.Class.Report(st) })
		}
	}
}
