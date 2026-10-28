//go:build js && wasm

package main

import (
	"math"
	"syscall/js"

	"github.com/go-widgets/mvvm"
	"github.com/go-widgets/webcanvas"

	"github.com/go-wasmcontainers/dashboard/ui"
	"github.com/go-wasmcontainers/dashboard/vm"
)

// GPU surfaces. The engine worker (go-wasmcontainers/gpu, webgpu.Browser
// OnSurface) posts, for each wasi-gfx surface a container opens:
//
//	{t:"surface", op:"open", surface, container, pid, width, height}
//	{t:"surface", op:"resize", surface, width, height}
//	{t:"surface", op:"close", surface}
//	{t:"frame", surface, seq, width, height, bitmap}   (bitmap transferred)
//
// and paces the guest on {t:"frame-ack", surface, seq}, which the dashboard
// posts once a frame is drawn or dropped. Each frame's ImageBitmap is drawn
// into the surface's backing canvas when the next scene frame is drawn,
// closed at once, and acknowledged; the pane is redrawn from the backing
// canvas, which also keeps the last frame of a closed surface.

// bitmap is a frame: an ImageBitmap this page now owns.
type bitmap struct{ v js.Value }

func (b bitmap) Close() {
	defer func() { _ = recover() }() // close() on anything odd must not stop the frame
	b.v.Call("close")
}

// u32 reads an integer field in [0, 2^32); ok false otherwise.
func u32(m js.Value, name string) (uint32, bool) {
	f := m.Get(name)
	if f.Type() != js.TypeNumber {
		return 0, false
	}
	n := f.Float()
	if n != math.Trunc(n) || n < 0 || n > math.MaxUint32 {
		return 0, false
	}
	return uint32(n), true
}

// seqOf reads a frame number: an integer from 1 to 2^53 (a JS number).
func seqOf(m js.Value) (uint64, bool) {
	f := m.Get("seq")
	if f.Type() != js.TypeNumber {
		return 0, false
	}
	n := f.Float()
	if n != math.Trunc(n) || n < 1 || n > 1<<53 {
		return 0, false
	}
	return uint64(n), true
}

// dim reads a width or height; 0 when absent or nonsense (vm bounds it).
func dim(m js.Value, name string) int {
	n, ok := u32(m, name)
	if !ok || n > 1<<16 {
		return 0
	}
	return int(n)
}

// backings is the page side of the surface panes: one canvas per surface.
type backings struct {
	doc      js.Value
	offscren bool
	canvases map[uint32]js.Value
}

func newBackings() *backings {
	g := js.Global()
	return &backings{doc: g.Get("document"), offscren: g.Get("OffscreenCanvas").Type() == js.TypeFunction,
		canvases: map[uint32]js.Value{}}
}

func (b *backings) Present(id uint32, f vm.Frame, w, h int) {
	defer f.Close()
	bm, ok := f.(bitmap)
	if !ok {
		return
	}
	c, ok := b.canvases[id]
	if !ok {
		if b.offscren {
			c = js.Global().Get("OffscreenCanvas").New(w, h)
		} else {
			c = b.doc.Call("createElement", "canvas")
		}
		b.canvases[id] = c
	}
	if c.Get("width").Int() != w || c.Get("height").Int() != h {
		c.Set("width", w)
		c.Set("height", h)
	}
	defer func() { _ = recover() }() // a detached bitmap throws: the frame is lost, not the page
	c.Call("getContext", "2d").Call("drawImage", bm.v, 0, 0, w, h)
}

func (b *backings) Image(id uint32) (webcanvas.Image, bool) {
	c, ok := b.canvases[id]
	if !ok {
		return webcanvas.Image{}, false
	}
	return webcanvas.NewImage(c), true
}

func (b *backings) Forget(id uint32) {
	if c, ok := b.canvases[id]; ok {
		c.Set("width", 0) // release its pixels now, not at the next collection
		c.Set("height", 0)
		delete(b.canvases, id)
	}
}

// connectSurfaces routes the worker's surface messages to the panes.
func connectSurfaces(worker js.Value, app *vm.App, q *mvvm.Queue, scene *ui.Scene) {
	scene.SetSurfaceCanvas(newBackings())
	ack := func(id uint32, seq uint64) {
		worker.Call("postMessage", map[string]any{"t": "frame-ack", "surface": id, "seq": float64(seq)})
	}
	app.Surfaces.SetAck(ack)
	imageBitmap := js.Global().Get("ImageBitmap")
	worker.Call("addEventListener", "message", js.FuncOf(func(_ js.Value, a []js.Value) any {
		m := a[0].Get("data")
		if m.Type() != js.TypeObject {
			return nil
		}
		switch str(m, "t") {
		case "surface":
			id, ok := u32(m, "surface")
			if !ok {
				return nil
			}
			switch str(m, "op") {
			case "open":
				pid, _ := u32(m, "pid")
				container, w, h := str(m, "container"), dim(m, "width"), dim(m, "height")
				q.Post(func() { app.Surfaces.Open(id, container, pid, w, h) })
			case "resize":
				w, h := dim(m, "width"), dim(m, "height")
				q.Post(func() { app.Surfaces.Resize(id, w, h) })
			case "close":
				q.Post(func() { app.Surfaces.Close(id) })
			}
		case "frame":
			bm := m.Get("bitmap")
			isBitmap := bm.Type() == js.TypeObject && imageBitmap.Type() == js.TypeFunction && bm.InstanceOf(imageBitmap)
			id, idOK := u32(m, "surface")
			seq, seqOK := seqOf(m)
			if !isBitmap || !idOK || !seqOK {
				// Not a frame this page can take: release what came, and
				// acknowledge what can be named so the guest is not stalled.
				if isBitmap {
					bitmap{bm}.Close()
				}
				if idOK && seqOK {
					ack(id, seq)
				}
				return nil
			}
			w, h := dim(m, "width"), dim(m, "height")
			f := bitmap{bm}
			q.Post(func() { app.Surfaces.Frame(id, seq, w, h, f) })
		}
		return nil
	}))
}
