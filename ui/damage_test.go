package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-widgets/webcanvas"
)

var _ webcanvas.DamageDrawer = (*Scene)(nil)

// settle draws frames until the engine's first reply is in and nothing is owed,
// then returns a framebuffer holding the settled frame.
func (r *rig) settle() []byte {
	r.t.Helper()
	r.until("engine", func(p Probe) bool { return strings.HasPrefix(p.Status[0], "engine test") })
	r.frame()
	return append([]byte(nil), r.buf...)
}

// whole is what a full repaint of the scene's current state shows.
func (r *rig) whole() []byte {
	buf := make([]byte, len(r.buf))
	r.s.drawWhole(buf)
	return buf
}

func area(rs []webcanvas.Rect) int {
	n := 0
	for _, r := range rs {
		n += r.W * r.H
	}
	return n
}

// One label's text changes: only that label is painted again and reported, and
// the buffer is what a full repaint shows.
func TestDamagedFrameOfOneLabelIsThatLabel(t *testing.T) {
	r := newRig(t)
	buf := r.settle()
	r.app.Error.Set("the engine said no")
	rects := r.s.DrawDamaged(buf)
	if len(rects) != 1 {
		t.Fatalf("rects %v, want the error label's", rects)
	}
	lb, m := r.s.errLabel.Bounds(), u(damageMargin)
	if got := rects[0]; got.X != lb.X-m || got.W != lb.W+2*m {
		t.Fatalf("damage %+v is not the label %+v plus its margin", got, lb)
	}
	if whole := r.s.w * r.s.h; area(rects)*50 > whole {
		t.Fatalf("one label damaged %d of %d pixels", area(rects), whole)
	}
	if !bytes.Equal(buf, r.whole()) {
		t.Fatal("the damaged frame differs from a full repaint")
	}
	// Shorter text must erase what the longer one drew.
	r.app.Error.Set("no")
	r.s.DrawDamaged(buf)
	if !bytes.Equal(buf, r.whole()) {
		t.Fatal("the shorter text left the longer one's pixels behind")
	}
}

// A frame nobody changed anything for (a guest's new image, a pointer move over
// a pane) repaints and reports nothing.
func TestFrameWithNothingChangedPaintsNothing(t *testing.T) {
	r := newRig(t)
	buf := r.settle()
	before := append([]byte(nil), buf...)
	if rects := r.s.DrawDamaged(buf); len(rects) != 0 {
		t.Fatalf("reported %v with nothing changed", rects)
	}
	if !bytes.Equal(buf, before) {
		t.Fatal("a frame with no damage changed the buffer")
	}
}

// An input event changes something nobody said where: the scene is repainted
// whole and the rectangles that differ are reported, so hovering one button
// puts that button, and a theme change puts everything.
func TestEventFramesReportWhatDiffers(t *testing.T) {
	r := newRig(t)
	buf := r.settle()
	// Type in the search field: its text, and what the filter does to the table.
	r.click("containers.search")
	r.typeText("x")
	rects := r.s.DrawDamaged(buf)
	if len(rects) == 0 || area(rects)*2 > r.s.w*r.s.h {
		t.Fatalf("typing reported %v: want rectangles covering less than half the canvas", rects)
	}
	if !bytes.Equal(buf, r.whole()) {
		t.Fatal("the typed frame differs from a full repaint")
	}
	// A theme change differs everywhere.
	r.s.toggleTheme()
	rects = r.s.DrawDamaged(buf)
	if area(rects)*2 < r.s.w*r.s.h {
		t.Fatalf("a theme change reported only %d of %d pixels", area(rects), r.s.w*r.s.h)
	}
	if !bytes.Equal(buf, r.whole()) {
		t.Fatal("the themed frame differs from a full repaint")
	}
}

// A guest's keys and pointer moves do not change the scene: they must not turn
// a layer-only frame into a repaint.
func TestPaneInputIsNotSceneDamage(t *testing.T) {
	r := newRig(t)
	r.settle()
	r.s.paneFocus = &pane{}
	r.s.locked = false
	for _, c := range []func() bool{func() bool { return r.s.Char("a") }, func() bool { return r.s.KeyDown("ArrowUp") }} {
		r.s.clearDamage()
		if !c() {
			t.Fatal("a key for the guest asks for no frame")
		}
		if r.s.full || r.s.unscoped {
			t.Fatal("a key for the guest marked the scene as changed")
		}
	}
}

// Damage that falls partly or wholly off the canvas is confined to it, and one
// rectangle another covers is not reported twice.
func TestMergeRects(t *testing.T) {
	canvas := rect{W: 100, H: 100}
	got := mergeRects([]rect{{X: -10, Y: -10, W: 30, H: 30}, {X: 5, Y: 5, W: 4, H: 4}, {X: 200, Y: 0, W: 5, H: 5}, {W: 0, H: 9}, {X: 50, Y: 50, W: 60, H: 60}, {X: 50, Y: 50, W: 60, H: 60}}, canvas)
	want := []rect{{W: 20, H: 20}, {X: 50, Y: 50, W: 50, H: 50}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("mergeRects = %v, want %v", got, want)
	}
}
