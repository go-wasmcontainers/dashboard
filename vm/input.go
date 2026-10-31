package vm

import (
	"math"
	"unicode/utf8"
)

// Box is a rectangle in canvas pixels.
type Box struct{ X, Y, W, H int }

// Fit is the largest box of aspect w:h centred in b: where a surface's
// frame is drawn in its pane (letterboxed), and where its input is read.
func Fit(b Box, w, h int) Box {
	if w <= 0 || h <= 0 || b.W <= 0 || b.H <= 0 {
		return Box{}
	}
	scale := math.Min(float64(b.W)/float64(w), float64(b.H)/float64(h))
	fw, fh := max(int(float64(w)*scale), 1), max(int(float64(h)*scale), 1)
	return Box{X: b.X + (b.W-fw)/2, Y: b.Y + (b.H-fh)/2, W: fw, H: fh}
}

// ToSurface maps canvas pixel (px, py) to surface pixels, for a surface of
// w×h drawn in img: the centre of the canvas pixel, scaled. A point outside
// the image (the letterbox, or outside the pane) is not the surface's: ok
// is false.
func ToSurface(img Box, w, h, px, py int) (x, y float64, ok bool) {
	if img.W <= 0 || img.H <= 0 || px < img.X || py < img.Y || px >= img.X+img.W || py >= img.Y+img.H {
		return 0, 0, false
	}
	return (float64(px-img.X) + 0.5) * float64(w) / float64(img.W), (float64(py-img.Y) + 0.5) * float64(h) / float64(img.H), true
}

// The kinds of a surface input event (go-wasmcontainers/gpu webgpu.InputKinds).
const (
	PointerDown = "pointer-down"
	PointerUp   = "pointer-up"
	PointerMove = "pointer-move"
	KeyDown     = "key-down"
	KeyUp       = "key-up"
)

// maxKeyText bounds a key event's text (webgpu.MaxKeyText).
const maxKeyText = 64

// SurfaceInput is one event for a surface's guest: {t:"input", ...}, which
// the engine worker hands to webgpu.SurfaceInput.
type SurfaceInput struct {
	Surface                uint32
	Kind                   string
	X, Y                   float64 // pointers: surface pixels
	Code, Text             string  // keys: KeyboardEvent.code, the text it produces
	Alt, Ctrl, Meta, Shift bool
}

// SetInput sets where surface input goes (the engine worker); nil drops it.
func (s *Surfaces) SetInput(send func(SurfaceInput)) { s.input = send }

// live reports whether surface id takes input: open, not closed.
func (s *Surfaces) live(id uint32) (*surface, bool) {
	sf, ok := s.byID[id]
	return sf, ok && !sf.row.Closed && s.input != nil
}

// Pointer sends a pointer event at canvas pixel (px, py) to surface id,
// drawn in img; outside the image it is dropped. Moves are coalesced: the
// last one waits for FlushMoves (once a frame), and any other event sends
// the waiting move first, so the guest sees them in order.
func (s *Surfaces) Pointer(id uint32, kind string, img Box, px, py int) {
	sf, ok := s.live(id)
	if !ok {
		return
	}
	x, y, in := ToSurface(img, sf.row.W, sf.row.H, px, py)
	if !in {
		return
	}
	ev := SurfaceInput{Surface: id, Kind: kind, X: x, Y: y}
	if kind == PointerMove {
		sf.move = &ev
		return
	}
	s.flushMove(sf)
	s.input(ev)
}

// Key sends a key event to surface id: its code, the text it produces
// (bounded; "" when it is too long), and the modifiers.
func (s *Surfaces) Key(id uint32, kind, code, text string, m KeyModifiers) {
	sf, ok := s.live(id)
	if !ok {
		return
	}
	if len(text) > maxKeyText || !utf8.ValidString(text) {
		text = ""
	}
	s.flushMove(sf)
	s.input(SurfaceInput{Surface: id, Kind: kind, Code: code, Text: text, Alt: m.Alt, Ctrl: m.Ctrl, Meta: m.Meta, Shift: m.Shift})
}

// KeyModifiers are the modifier keys of a key event.
type KeyModifiers struct{ Alt, Ctrl, Meta, Shift bool }

// FlushMoves sends the pointer moves that waited for this frame.
func (s *Surfaces) FlushMoves() {
	for _, id := range s.IDs() {
		if sf, ok := s.live(id); ok {
			s.flushMove(sf)
		}
	}
}

func (s *Surfaces) flushMove(sf *surface) {
	if sf.move != nil {
		ev := *sf.move
		sf.move = nil
		s.input(ev)
	}
}
