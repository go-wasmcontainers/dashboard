package vm

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestFit(t *testing.T) {
	for _, c := range []struct {
		b    Box
		w, h int
		want Box
	}{
		{Box{0, 0, 200, 100}, 640, 480, Box{33, 0, 133, 100}},    // pillarbox
		{Box{10, 10, 100, 400}, 640, 480, Box{10, 172, 100, 75}}, // letterbox
		{Box{0, 0, 640, 480}, 640, 480, Box{0, 0, 640, 480}},     // exact
		{Box{0, 0, 100, 100}, 0, 480, Box{}},
		{Box{0, 0, 0, 100}, 640, 480, Box{}},
		{Box{0, 0, 1, 1}, 1920, 1, Box{0, 0, 1, 1}},
	} {
		if got := Fit(c.b, c.w, c.h); got != c.want {
			t.Errorf("Fit(%v, %d, %d) = %v, want %v", c.b, c.w, c.h, got, c.want)
		}
	}
}

func TestToSurface(t *testing.T) {
	// A 640x480 surface drawn at half size, letterboxed in a taller pane.
	img := Fit(Box{100, 50, 320, 400}, 640, 480)
	if img != (Box{100, 130, 320, 240}) {
		t.Fatalf("image = %v", img)
	}
	for _, c := range []struct {
		px, py int
		x, y   float64
		ok     bool
	}{
		{100, 130, 1, 1, true},     // the first pixel's centre, doubled
		{419, 369, 639, 479, true}, // the last
		{260, 250, 321, 241, true}, // the middle
		{100, 129, 0, 0, false},    // the letterbox above
		{260, 370, 0, 0, false},    // below
		{99, 200, 0, 0, false},     // left of the pane
		{420, 200, 0, 0, false},    // right
	} {
		x, y, ok := ToSurface(img, 640, 480, c.px, c.py)
		if ok != c.ok || ok && (x != c.x || y != c.y) {
			t.Errorf("ToSurface(%d,%d) = %v,%v %v, want %v,%v %v", c.px, c.py, x, y, ok, c.x, c.y, c.ok)
		}
	}
	if _, _, ok := ToSurface(Box{}, 640, 480, 0, 0); ok {
		t.Error("an empty image took a point")
	}
}

func TestSurfaceInput(t *testing.T) {
	h := newHarness(t)
	s := h.app.Surfaces
	var got []string
	img := Box{0, 0, 320, 240}
	s.Open(7, "", 1, 640, 480)
	// No sink yet: nothing is sent, nothing breaks.
	s.Pointer(7, PointerDown, img, 10, 10)
	s.Key(7, KeyDown, "KeyA", "a", KeyModifiers{})
	s.FlushMoves()
	s.SetInput(func(ev SurfaceInput) {
		if ev.Kind == KeyDown || ev.Kind == KeyUp {
			got = append(got, fmt.Sprintf("%d %s %s %q alt=%v ctrl=%v meta=%v shift=%v", ev.Surface, ev.Kind, ev.Code, ev.Text, ev.Alt, ev.Ctrl, ev.Meta, ev.Shift))
			return
		}
		got = append(got, fmt.Sprintf("%d %s %g,%g", ev.Surface, ev.Kind, ev.X, ev.Y))
	})

	// Moves are coalesced to the last one per frame, and sent before any
	// other event, so the order holds.
	s.Pointer(7, PointerMove, img, 10, 10)
	s.Pointer(7, PointerMove, img, 20, 20)
	s.FlushMoves()
	s.FlushMoves() // nothing more
	s.Pointer(7, PointerMove, img, 30, 30)
	s.Pointer(7, PointerDown, img, 40, 40)
	s.Pointer(7, PointerMove, img, 50, 50)
	s.Key(7, KeyDown, "KeyA", "A", KeyModifiers{Shift: true})
	s.Key(7, KeyUp, "KeyA", "A", KeyModifiers{Shift: true, Alt: true, Ctrl: true, Meta: true})
	s.Pointer(7, PointerUp, img, 400, 10) // outside the image: dropped
	s.Pointer(7, PointerUp, img, 41, 41)
	want := []string{
		"7 pointer-move 41,41",
		"7 pointer-move 61,61",
		"7 pointer-down 81,81",
		"7 pointer-move 101,101",
		`7 key-down KeyA "A" alt=false ctrl=false meta=false shift=true`,
		`7 key-up KeyA "A" alt=true ctrl=true meta=true shift=true`,
		"7 pointer-up 83,83",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Text is bounded and valid UTF-8, or it is "".
	got = nil
	s.Key(7, KeyDown, "KeyB", strings.Repeat("x", maxKeyText+1), KeyModifiers{})
	s.Key(7, KeyDown, "KeyC", "\xff", KeyModifiers{})
	s.Key(7, KeyDown, "Enter", "", KeyModifiers{})
	if len(got) != 3 || !strings.Contains(got[0], `KeyB ""`) || !strings.Contains(got[1], `KeyC ""`) || !strings.Contains(got[2], `Enter ""`) {
		t.Errorf("bounded text: %q", got)
	}

	// Unknown and closed surfaces take nothing; a closed one's waiting move
	// is dropped with it.
	got = nil
	s.Pointer(9, PointerDown, img, 1, 1)
	s.Key(9, KeyDown, "KeyA", "a", KeyModifiers{})
	s.Pointer(7, PointerMove, img, 5, 5)
	s.Close(7)
	s.FlushMoves()
	s.Pointer(7, PointerDown, img, 1, 1)
	s.Key(7, KeyDown, "KeyA", "a", KeyModifiers{})
	if len(got) != 0 {
		t.Errorf("sent to an unknown or closed surface: %q", got)
	}
}

// Mouse buttons, relative motion and the pointer lock wish (gpu v0.14.0).
func TestSurfaceMouse(t *testing.T) {
	h := newHarness(t)
	s := h.app.Surfaces
	var got []string
	s.SetInput(func(ev SurfaceInput) {
		switch ev.Kind {
		case PointerMotion:
			got = append(got, fmt.Sprintf("%s %g,%g", ev.Kind, ev.DX, ev.DY))
		case ButtonDown, ButtonUp:
			got = append(got, fmt.Sprintf("%s %d %g,%g", ev.Kind, ev.Button, ev.X, ev.Y))
		default:
			got = append(got, fmt.Sprintf("%s %g,%g", ev.Kind, ev.X, ev.Y))
		}
	})
	img := Box{0, 0, 320, 240} // a 640x480 surface at half size
	s.Open(7, "", 1, 640, 480)

	// The wish is the guest's, per surface; an unknown or closed one has none.
	if s.Capture(7) {
		t.Error("a new surface wants the lock")
	}
	s.SetCapture(7, true)
	s.SetCapture(9, true)
	if !s.Capture(7) || s.Capture(9) {
		t.Errorf("capture: 7 %v, 9 %v", s.Capture(7), s.Capture(9))
	}

	// Motion is scaled to surface pixels and summed over the frame: one event
	// a frame, and none when nothing moved.
	s.Motion(7, img, 3, -1)
	s.Motion(7, img, 2, 0.5)
	s.FlushMoves()
	s.FlushMoves()
	// A button sends the waiting motion first, so the guest sees them in order.
	s.Motion(7, img, 1, 1)
	s.Button(7, ButtonDown, 2, img, 160, 120)
	s.Button(7, ButtonUp, 2, img, 400, 10) // outside the image: dropped
	s.Button(7, ButtonUp, 2, img, 160, 120)
	s.Button(7, PointerDown, 0, img, 160, 120) // not a button kind
	s.Motion(7, img, 0, 0)                     // nothing moved
	s.Motion(7, img, math.Inf(1), 0)           // not finite: dropped
	s.FlushMoves()
	want := []string{
		"pointer-motion 10,-1",
		"pointer-motion 2,2",
		"button-down 2 321,241",
		"button-up 2 321,241",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// A closed surface takes nothing and wants nothing.
	got = nil
	s.Motion(7, img, 5, 5)
	s.Close(7)
	s.FlushMoves()
	s.Button(7, ButtonDown, 0, img, 1, 1)
	if len(got) != 0 || s.Capture(7) {
		t.Errorf("a closed surface: %q, capture %v", got, s.Capture(7))
	}
}
