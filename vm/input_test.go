package vm

import (
	"fmt"
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
