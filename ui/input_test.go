package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-widgets/webcanvas"

	"github.com/go-wasmcontainers/dashboard/vm"
)

var (
	_ webcanvas.KeyCodeAware = (*Scene)(nil)
	_ webcanvas.KeyUpper     = (*Scene)(nil)
)

// Surface input through the scene: a click focuses the pane and is mapped
// to surface pixels; keys go to the focused pane only, with their code;
// moves are coalesced to the frame; F6 or a widget takes the focus back.
func TestSurfaceInputScene(t *testing.T) {
	r := newRig(t)
	r.until("engine", func(p Probe) bool { return strings.HasPrefix(p.Status[0], "engine test") })
	sf := r.app.Surfaces
	var got []string
	sf.SetInput(func(ev vm.SurfaceInput) {
		if ev.Kind == vm.KeyDown || ev.Kind == vm.KeyUp {
			got = append(got, fmt.Sprintf("%s %s %q shift=%v", ev.Kind, ev.Code, ev.Text, ev.Shift))
			return
		}
		got = append(got, fmt.Sprintf("%s %.0f,%.0f", ev.Kind, ev.X, ev.Y))
	})
	r.s.SetSurfaceCanvas(&backing{images: map[uint32]string{}})
	sf.Open(1, "", 7, 640, 480)
	r.app.View.Set(vm.ViewSurfaces)
	r.frame()
	pn := r.s.panes[0]
	img := r.s.image(pn)

	// Keys before any pane has the focus go to the widgets, not the guest.
	r.s.KeyCode("KeyA")
	r.s.Char("a")
	r.s.KeyUp("a")
	if len(got) != 0 {
		t.Fatalf("keys reached the guest without the focus: %q", got)
	}

	// A press in the letterbox focuses the pane but is not the surface's.
	if pn.body.Y < img.Y {
		r.s.Click(img.X+1, pn.body.Y)
		r.s.Release(img.X+1, pn.body.Y)
	}
	// A press on the frame's top-left pixel: (0.5,0.5) scaled.
	r.s.Click(img.X, img.Y)
	if r.s.paneFocus != pn || r.s.focus != nil {
		t.Fatal("the pane did not take the focus")
	}
	r.s.Release(img.X+img.W-1, img.Y+img.H-1)
	// Shift+A, then its release; a named key; F6 leaves.
	r.s.Modifiers(webcanvas.Modifiers{Shift: true})
	r.s.KeyCode("KeyA")
	r.s.Char("A")
	r.s.KeyCode("KeyA")
	r.s.KeyUp("A")
	r.s.Modifiers(webcanvas.Modifiers{})
	r.s.KeyCode("ArrowUp")
	r.s.KeyDown("ArrowUp")
	r.s.KeyCode("Tab")
	r.s.KeyDown("Tab") // the guest's, as in a terminal
	// Moves: three in a frame, one sent.
	r.s.Move(img.X+10, img.Y+10)
	r.s.Move(img.X+20, img.Y+20)
	r.s.Move(img.X+30, img.Y+30)
	r.s.Layers()
	r.s.Layers()
	r.s.KeyCode("F6")
	r.s.KeyDown("F6")
	if r.s.paneFocus != nil {
		t.Error("F6 did not leave the pane")
	}
	r.s.Char("x") // the widgets' again
	r.frame()

	// The coordinates the guest gets: the canvas pixel's centre scaled to
	// the 640x480 surface (vm.ToSurface, whose math vm's tests pin).
	at := func(kind string, px, py int) string {
		x, y, ok := vm.ToSurface(img, 640, 480, px, py)
		if !ok {
			t.Fatalf("(%d,%d) is outside the image", px, py)
		}
		return fmt.Sprintf("%s %.0f,%.0f", kind, x, y)
	}
	want := []string{
		at("pointer-down", img.X, img.Y),
		at("pointer-up", img.X+img.W-1, img.Y+img.H-1),
		`key-down KeyA "A" shift=true`,
		`key-up KeyA "A" shift=true`,
		`key-down ArrowUp "" shift=false`,
		`key-down Tab "" shift=false`,
		at("pointer-move", img.X+30, img.Y+30),
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// A click on a widget takes the focus back from a pane.
	r.s.Click(img.X, img.Y)
	r.s.Release(img.X, img.Y)
	r.click("theme")
	if r.s.paneFocus != nil {
		t.Error("a widget click left the pane focused")
	}
	// The focused pane is framed; a pane that goes away drops its focus.
	r.s.Click(img.X, img.Y)
	r.frame()
	sf.Dismiss(1)
	if r.s.paneFocus != nil || r.s.paneDrag != nil {
		t.Error("a dismissed pane kept the focus")
	}
	// Another view: a press there is not a pane's.
	r.app.View.Set(vm.ViewContainers)
	if r.s.paneAt(img.X, img.Y) != nil {
		t.Error("a pane answered in another view")
	}
	r.frame()
}

// The pointer lock: a press on a pane whose guest asked for it grabs the mouse
// without being the guest's; locked, the pane gets relative motion, every
// button and no absolute moves; Escape (the browser) unlocks it.
func TestSurfaceMouseLockScene(t *testing.T) {
	r := newRig(t)
	r.until("engine", func(p Probe) bool { return strings.HasPrefix(p.Status[0], "engine test") })
	sf := r.app.Surfaces
	var got []string
	sf.SetInput(func(ev vm.SurfaceInput) {
		switch ev.Kind {
		case vm.PointerMotion:
			got = append(got, fmt.Sprintf("%s %.1f,%.1f", ev.Kind, ev.DX, ev.DY))
		case vm.ButtonDown, vm.ButtonUp:
			got = append(got, fmt.Sprintf("%s %d", ev.Kind, ev.Button))
		default:
			got = append(got, ev.Kind)
		}
	})
	r.s.SetSurfaceCanvas(&backing{images: map[uint32]string{}})
	sf.Open(1, "", 7, 640, 480)
	r.app.View.Set(vm.ViewSurfaces)
	r.frame()
	img := r.s.image(r.s.panes[0])
	cx, cy := img.X+img.W/2, img.Y+img.H/2

	// The guest has not asked: no lock, and a press is the guest's.
	if r.s.WantsPointerLock(cx, cy) {
		t.Fatal("a pane whose guest did not ask wants the lock")
	}
	r.s.MouseButton(true, 2, cx, cy)
	r.s.MouseButton(false, 2, cx, cy)
	r.s.MouseMotion(5, 5) // not locked: not sent
	r.s.Layers()
	if strings.Join(got, ",") != "button-down 2,button-up 2" {
		t.Fatalf("before the lock: %q", got)
	}

	// The guest asks: the next press on the pane wants the lock; outside any
	// pane, or on a widget, it does not.
	got = nil
	sf.SetCapture(1, true)
	if !r.s.WantsPointerLock(cx, cy) || r.s.WantsPointerLock(0, 0) {
		t.Fatal("WantsPointerLock does not follow the guest's request and the pane")
	}
	r.s.GrabPointer(cx, cy)
	if r.s.paneFocus != r.s.panes[0] || len(got) != 0 {
		t.Fatalf("the grab must focus the pane and send nothing: %q", got)
	}
	r.s.SetPointerLocked(true)
	if r.s.WantsPointerLock(cx, cy) || !r.s.PointerLocked() || r.s.ShouldReleasePointer() {
		t.Fatal("locked state")
	}

	// Locked: absolute moves are not the guest's; motion is summed to one a
	// frame, scaled from canvas to surface pixels; buttons go to the frame's
	// centre.
	scale := 640 / float64(img.W)
	r.s.Move(cx+1, cy+1)
	r.s.MouseMotion(2, 1)
	r.s.MouseMotion(3, -1)
	r.s.MouseButton(true, 0, 0, 0)
	r.s.MouseButton(false, 0, 0, 0)
	r.s.Layers()
	want := []string{
		fmt.Sprintf("pointer-motion %.1f,%.1f", 5*scale, 0*scale),
		"button-down 0",
		"button-up 0",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("locked:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// The browser released it (Escape): absolute moves are the guest's again.
	got = nil
	r.s.SetPointerLocked(false)
	r.s.Move(cx, cy)
	r.s.MouseMotion(9, 9)
	r.s.Layers()
	if strings.Join(got, ",") != "pointer-move" {
		t.Errorf("after the release: %q", got)
	}

	// A guest that stops wanting the lock, or a pane that goes, frees a locked
	// pointer.
	r.s.SetPointerLocked(true)
	sf.SetCapture(1, false)
	if !r.s.ShouldReleasePointer() {
		t.Error("a guest that no longer wants the lock keeps it")
	}
	sf.SetCapture(1, true)
	sf.Close(1)
	r.frame()
	if !r.s.ShouldReleasePointer() {
		t.Error("a closed pane keeps the lock")
	}
}
