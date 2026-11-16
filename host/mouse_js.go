//go:build js && wasm

package host

import (
	"syscall/js"

	"github.com/go-wasmcontainers/dashboard/ui"
)

// ConnectMouse gives a surface pane's guest the mouse: every button
// (MouseEvent.button) and, with the pointer locked to the canvas, relative
// motion (movementX/Y). The webcanvas host forwards only the left button and
// absolute positions, so this listens beside it on the #screen canvas.
//
// A guest that asked for the pointer (gpu's request-pointer-lock, reported as
// {t:"surface", op:"capture"}) gets it locked on the next press on its pane: a
// browser grants requestPointerLock only from a user gesture. That press only
// grabs the mouse; it is not sent to the guest (a click that grabs must not
// also press a menu row), and neither is its release. The browser releases the
// lock on Escape; the pane then sends absolute moves again, and the next press
// locks it once more.
func ConnectMouse(scene *ui.Scene) {
	g := js.Global()
	doc := g.Get("document")
	canvas := doc.Call("getElementById", "screen")
	if !canvas.Truthy() || !canvas.Get("requestPointerLock").Truthy() {
		return
	}
	// The webcanvas mapping: client pixels to canvas pixels, scaled for a CSS
	// resize (the canvas keeps its intrinsic size).
	ratio := func() (sx, sy float64) {
		r := canvas.Call("getBoundingClientRect")
		sx, sy = canvas.Get("width").Float()/r.Get("width").Float(), canvas.Get("height").Float()/r.Get("height").Float()
		if !(sx > 0) || !(sy > 0) {
			return 1, 1
		}
		return sx, sy
	}
	coords := func(ev js.Value) (int, int) {
		r := canvas.Call("getBoundingClientRect")
		sx, sy := ratio()
		return int((ev.Get("clientX").Float() - r.Get("left").Float()) * sx), int((ev.Get("clientY").Float() - r.Get("top").Float()) * sy)
	}
	onCanvas := func(ev js.Value) bool {
		t := ev.Get("target")
		return t.Truthy() && t.Equal(canvas)
	}
	swallow := false // the press that grabbed the pointer: its release is not the guest's either
	// Capture phase on the window: this sees a press before the canvas's own
	// listeners, and can keep the grabbing press from them.
	g.Call("addEventListener", "mousedown", js.FuncOf(func(_ js.Value, a []js.Value) any {
		ev := a[0]
		if !onCanvas(ev) {
			return nil
		}
		x, y := coords(ev)
		button := ev.Get("button").Int()
		if button == 0 && scene.WantsPointerLock(x, y) {
			canvas.Call("requestPointerLock")
			scene.GrabPointer(x, y)
			swallow = true
			ev.Call("stopPropagation")
			ev.Call("preventDefault")
			return nil
		}
		scene.MouseButton(true, button, x, y)
		return nil
	}), true)
	g.Call("addEventListener", "mouseup", js.FuncOf(func(_ js.Value, a []js.Value) any {
		ev := a[0]
		if !onCanvas(ev) && !scene.PointerLocked() {
			return nil
		}
		button := ev.Get("button").Int()
		if swallow && button == 0 {
			swallow = false
			ev.Call("stopPropagation")
			return nil
		}
		x, y := coords(ev)
		scene.MouseButton(false, button, x, y)
		return nil
	}), true)
	g.Call("addEventListener", "mousemove", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if !scene.PointerLocked() {
			return nil
		}
		if scene.ShouldReleasePointer() {
			doc.Call("exitPointerLock")
			return nil
		}
		ev := a[0]
		sx, sy := ratio()
		scene.MouseMotion(ev.Get("movementX").Float()*sx, ev.Get("movementY").Float()*sy)
		return nil
	}), false)
	doc.Call("addEventListener", "pointerlockchange", js.FuncOf(func(js.Value, []js.Value) any {
		el := doc.Get("pointerLockElement")
		scene.SetPointerLocked(el.Truthy() && el.Equal(canvas))
		return nil
	}))
}
