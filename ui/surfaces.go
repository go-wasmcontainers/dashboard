package ui

import (
	"math"

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
	for id := range old {
		if !sf.Has(id) && s.surfaceCanvas != nil {
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
// before the first frame; the frame itself is a layer drawn over it.
func (s *Scene) drawSurfaceGrounds(p painter.Painter) {
	for _, pn := range s.panes {
		p.FillRect(pn.body, toolkit.RGB(0x10, 0x12, 0x16))
		if !pn.shown {
			toolkit.DrawText(p, pn.body.X+u(gap), pn.body.Y+u(gap), "waiting for first frame", toolkit.RGB(0xC8, 0xCC, 0xD2))
		}
	}
}

// fit is the largest rectangle of aspect w:h centred in r.
func fit(r rect, w, h int) rect {
	if w <= 0 || h <= 0 || r.W <= 0 || r.H <= 0 {
		return rect{}
	}
	scale := math.Min(float64(r.W)/float64(w), float64(r.H)/float64(h))
	fw, fh := max(int(float64(w)*scale), 1), max(int(float64(h)*scale), 1)
	return rect{X: r.X + (r.W-fw)/2, Y: r.Y + (r.H-fh)/2, W: fw, H: fh}
}

// Layers implements webcanvas.Layered. Every frame waiting is presented
// first -- drawn into its surface's backing image, closed, acknowledged --
// whether or not the Surfaces view is shown, so the engine never waits on a
// pane nobody looks at. Then, in the Surfaces view, each pane's backing
// image is drawn over its frame area, scaled to fit with its aspect kept,
// greyed when the surface is closed.
func (s *Scene) Layers() []webcanvas.Layer {
	sf := s.app.Surfaces
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
