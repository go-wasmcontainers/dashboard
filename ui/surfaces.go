package ui

import (
	"math"
	"unicode/utf8"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
	"github.com/go-widgets/webcanvas"

	"github.com/go-wasmcontainers/dashboard/vm"
)

// SurfaceCanvas keeps one backing image per GPU surface: the page's side of
// the surface panes. Present draws a frame into the surface's backing image
// and closes the frame, whatever happens; Image is that backing image (false
// before the first frame); Forget releases it. In the browser the backing
// image is a canvas and a frame an ImageBitmap (cmd/dashboard).
type SurfaceCanvas interface {
	Present(id uint32, f vm.Frame, w, h int)
	Image(id uint32) (webcanvas.Image, bool)
	Forget(id uint32)
}

// closedAlpha is how a closed surface's last frame is shown: greyed.
const closedAlpha = 0.35

// pane is one surface's widgets.
type pane struct {
	id      uint32
	title   *toolkit.Label
	status  *toolkit.Label
	dismiss *toolkit.Button
	body    rect
	closed  bool
	shown   bool
}

// SetSurfaceCanvas connects the page's backing images (nil: frames are
// dropped as they come, and acknowledged).
func (s *Scene) SetSurfaceCanvas(c SurfaceCanvas) { s.surfaceCanvas = c }

func (s *Scene) buildSurfaces() {
	inv := s.invalidate
	sf := s.app.Surfaces
	s.sEmpty = toolkit.NewLabel("No GPU surfaces. A container that draws with wasi-gfx shows its surface here.")
	sf.Rows.SubscribeChanged(func() { s.syncPanes(); s.layout(); inv() })
	s.syncPanes()
}

// syncPanes makes one pane per surface row, reusing the widgets of the
// panes that stay.
func (s *Scene) syncPanes() {
	sf := s.app.Surfaces
	old := map[uint32]*pane{}
	for _, p := range s.panes {
		old[p.id] = p
	}
	s.panes = s.panes[:0]
	for _, r := range sf.Rows.Slice() {
		p, ok := old[r.ID]
		if !ok {
			id := r.ID
			p = &pane{id: id, title: toolkit.NewLabel(""), status: toolkit.NewLabel("")}
			// Dismissing removes the pane; syncPanes then forgets its image.
			p.dismiss = button("Dismiss", func() { sf.Dismiss(id) })
		}
		p.title.Text().Set(r.Title)
		p.status.Text().Set(r.Status)
		p.closed, p.shown = r.Closed, r.Shown
		s.panes = append(s.panes, p)
	}
	for id, p := range old {
		if sf.Has(id) {
			continue
		}
		if p == s.paneFocus {
			s.paneFocus = nil
		}
		if p == s.paneDrag {
			s.paneDrag = nil
		}
		if s.surfaceCanvas != nil {
			s.surfaceCanvas.Forget(id)
		}
	}
}

// layoutSurfaces tiles the panes over the content area.
func (s *Scene) layoutSurfaces(c rect) {
	g := u(gap)
	line := u(22)
	s.sEmpty.SetBounds(rect{X: c.X, Y: c.Y, W: c.W, H: line})
	n := len(s.panes)
	if n == 0 {
		return
	}
	cols := int(math.Ceil(math.Sqrt(float64(n))))
	rows := (n + cols - 1) / cols
	cw := (c.W - (cols-1)*g) / cols
	ch := (c.H - (rows-1)*g) / rows
	for i, p := range s.panes {
		x := c.X + (i%cols)*(cw+g)
		y := c.Y + (i/cols)*(ch+g)
		p.title.SetBounds(rect{X: x, Y: y, W: cw, H: line})
		if p.closed {
			row(rect{X: x, Y: y + line, W: cw, H: line}, p.status, 0, p.dismiss, 90)
		} else {
			p.status.SetBounds(rect{X: x, Y: y + line, W: cw, H: line})
		}
		p.body = rect{X: x, Y: y + 2*line + g/2, W: cw, H: max(ch-2*line-g/2, 1)}
	}
}

// surfaceWidgets are the panes' interactive widgets (the Dismiss buttons of
// closed surfaces).
func (s *Scene) surfaceWidgets() []toolkit.Widget {
	var out []toolkit.Widget
	for _, p := range s.panes {
		if p.closed {
			out = append(out, p.dismiss)
		}
	}
	return out
}

// surfaceLabels are the panes' titles and statuses.
func (s *Scene) surfaceLabels() []toolkit.Widget {
	if len(s.panes) == 0 {
		return []toolkit.Widget{s.sEmpty}
	}
	var out []toolkit.Widget
	for _, p := range s.panes {
		out = append(out, p.title, p.status)
	}
	return out
}

// drawSurfaceGrounds paints each pane's frame area, with its wait text
// before the first frame; the frame itself is a layer drawn over it. The
// pane that has the focus (its guest gets the keys) is framed in the accent.
func (s *Scene) drawSurfaceGrounds(p painter.Painter) {
	for _, pn := range s.panes {
		if pn == s.paneFocus {
			b := pn.body
			w := max(u(2), 1)
			p.FillRect(rect{X: b.X - w, Y: b.Y - w, W: b.W + 2*w, H: b.H + 2*w}, s.theme.Accent)
		}
		p.FillRect(pn.body, toolkit.RGB(0x10, 0x12, 0x16))
		if !pn.shown {
			toolkit.DrawText(p, pn.body.X+u(gap), pn.body.Y+u(gap), "waiting for first frame", toolkit.RGB(0xC8, 0xCC, 0xD2))
		}
	}
}

// fit is the largest rectangle of aspect w:h centred in r (vm.Fit).
func fit(r rect, w, h int) rect {
	b := vm.Fit(vm.Box{X: r.X, Y: r.Y, W: r.W, H: r.H}, w, h)
	return rect{X: b.X, Y: b.Y, W: b.W, H: b.H}
}

// image is where pane p's frame is drawn (and its input read).
func (s *Scene) image(p *pane) vm.Box {
	r, _ := s.app.Surfaces.Find(p.id)
	d := fit(p.body, r.W, r.H)
	return vm.Box{X: d.X, Y: d.Y, W: d.W, H: d.H}
}

// paneAt is the pane whose frame area holds (x, y), in the Surfaces view.
func (s *Scene) paneAt(x, y int) *pane {
	if s.app.View.Get() != vm.ViewSurfaces {
		return nil
	}
	for _, p := range s.panes {
		if p.body.Contains(x, y) {
			return p
		}
	}
	return nil
}

// pointer sends a pointer event at (x, y) to the pane under it, if any;
// pointer-down also gives that pane the focus (and takes it from the
// widgets). It reports whether the point was a pane's.
func (s *Scene) pointer(kind string, x, y int) bool {
	p := s.paneAt(x, y)
	if p == nil {
		return false
	}
	if kind == vm.PointerDown {
		s.setFocus(nil)
		s.paneFocus = p
		s.invalidate()
	}
	if !p.closed {
		s.app.Surfaces.Pointer(p.id, kind, s.image(p), x, y)
	}
	return true
}

// key sends a key event to the focused pane's guest.
func (s *Scene) key(kind, text string) {
	p := s.paneFocus
	if p == nil {
		return
	}
	s.app.Surfaces.Key(p.id, kind, s.code, text, vm.KeyModifiers{Alt: s.mods.Alt, Ctrl: s.mods.Ctrl, Meta: s.mods.Meta, Shift: s.mods.Shift})
}

// KeyCode implements webcanvas.KeyCodeAware: the physical key of the
// keyboard event about to be delivered.
func (s *Scene) KeyCode(code string) { s.code = code }

// KeyUp implements webcanvas.KeyUpper: a release goes to the focused
// pane's guest, with the text the key produces when it is one character.
func (s *Scene) KeyUp(key string) bool {
	if s.paneFocus == nil {
		return false
	}
	s.key(vm.KeyUp, oneChar(key))
	return true
}

// oneChar is key when it is one character (KeyboardEvent.key for a key
// that produces text), else "" (a named key: "Enter", "ArrowUp").
func oneChar(key string) string {
	if utf8.RuneCountInString(key) == 1 {
		return key
	}
	return ""
}

// Layers implements webcanvas.Layered. Every frame waiting is presented
// first -- drawn into its surface's backing image, closed, acknowledged --
// whether or not the Surfaces view is shown, so the engine never waits on a
// pane nobody looks at. Then, in the Surfaces view, each pane's backing
// image is drawn over its frame area, scaled to fit with its aspect kept,
// greyed when the surface is closed.
func (s *Scene) Layers() []webcanvas.Layer {
	sf := s.app.Surfaces
	sf.FlushMoves() // at most one pointer move per surface per frame
	for _, id := range sf.IDs() {
		f, seq, w, h, ok := sf.Take(id)
		if !ok {
			continue
		}
		if s.surfaceCanvas != nil {
			s.surfaceCanvas.Present(id, f, w, h)
		} else {
			f.Close()
		}
		sf.Presented(id, seq, w, h)
	}
	if s.app.View.Get() != vm.ViewSurfaces || s.surfaceCanvas == nil {
		return nil
	}
	var out []webcanvas.Layer
	for _, p := range s.panes {
		img, ok := s.surfaceCanvas.Image(p.id)
		if !ok {
			continue
		}
		r, _ := sf.Find(p.id)
		d := fit(p.body, r.W, r.H)
		l := webcanvas.Layer{Image: img, X: d.X, Y: d.Y, W: d.W, H: d.H}
		if p.closed {
			l.Alpha = closedAlpha
		}
		out = append(out, l)
	}
	return out
}
