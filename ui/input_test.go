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
