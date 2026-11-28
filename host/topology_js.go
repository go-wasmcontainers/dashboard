//go:build js && wasm

package host

import (
	"syscall/js"

	"github.com/go-widgets/mvvm"

	"github.com/go-wasmcontainers/dashboard/vm"
)

// Limits on what the page may report as sounding containers: the page is not
// trusted with the size of the list.
const (
	maxActive      = 256
	maxActiveLabel = 128
)

// ConnectTopology joins the Topology view to its page:
//
//   - the page-wide mute reaches window.wctrAudio.setGlobalMute(muted);
//   - the page reports which containers have a sound stream by calling
//     window.__wctrSoundActive(["name or id", ...]) (the playground's
//     web/audio.js), which the speakers of the graph follow;
//   - a page that offers window.__wctrCompose = {pick(), export(project)}
//     enables Import compose and Export compose (a file picker, a download).
//
// A page without them leaves the controls disabled or inert.
func ConnectTopology(g js.Value, app *vm.App, q *mvvm.Queue) {
	app.Sound.SetGlobalAudio(func(muted bool) {
		a := g.Get("wctrAudio")
		if a.Type() == js.TypeObject && a.Get("setGlobalMute").Type() == js.TypeFunction {
			a.Call("setGlobalMute", muted)
		}
	})
	g.Set("__wctrSoundActive", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) == 0 || a[0].Type() != js.TypeObject {
			return nil
		}
		list := a[0]
		n := list.Length()
		if n > maxActive {
			n = maxActive
		}
		ids := make([]string, 0, n)
		for i := 0; i < n; i++ {
			if v := list.Index(i); v.Type() == js.TypeString {
				if s := v.String(); len(s) <= maxActiveLabel {
					ids = append(ids, s)
				}
			}
		}
		q.Post(func() { app.Sound.SetActive(ids) })
		return nil
	}))

	page := g.Get("__wctrCompose")
	if page.Type() != js.TypeObject || page.Get("pick").Type() != js.TypeFunction || page.Get("export").Type() != js.TypeFunction {
		return
	}
	q.Post(func() {
		app.Topology.SetCompose(
			func() { page.Call("pick") },
			func(project string) { page.Call("export", project) },
		)
	})
}
