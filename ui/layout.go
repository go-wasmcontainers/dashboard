package ui

import (
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
	"github.com/go-widgets/webcanvas"

	"github.com/go-wasmcontainers/dashboard/vm"
)

type rect = toolkit.Rect

// Size implements webcanvas.App.
func (s *Scene) Size() (int, int) { return s.w, s.h }

// u converts a layout length in logical pixels to device pixels at the
// toolkit's metric scale (the display's pixel ratio).
func u(v int) int { return toolkit.Scaled(v) }

// Resize implements webcanvas.Resizer: the scene fills the canvas (device
// pixels), at least minW x minH logical pixels.
func (s *Scene) Resize(w, h int) (int, int) {
	s.w, s.h = max(w, u(minW)), max(h, u(minH))
	s.layout()
	s.dirty = true
	return s.w, s.h
}

// SetPixelRatio implements webcanvas.PixelRatioAware: the scene renders at
// the display's device pixels. Every toolkit metric and the OpenType text
// scale with the ratio (toolkit.SetMetricScale), and the terminals' monospace
// face is re-rendered at its size × ratio.
func (s *Scene) SetPixelRatio(r float64) {
	if r == toolkit.MetricScale() {
		return
	}
	toolkit.SetMetricScale(r)
	f := monoFace(r)
	s.term.Font, s.logs.Font = f, f
	s.nav.RowHeight = u(navRowH)
	s.dirty = true
}

// RepaintWith implements webcanvas.RepaintAware.
func (s *Scene) RepaintWith(request func()) { s.repaint = request }

// Modifiers implements webcanvas.ModifierAware.
func (s *Scene) Modifiers(m webcanvas.Modifiers) { s.mods = m }

// row lays widgets left to right in r, each with its width in logical
// pixels (0: a share of the rest).
func row(r rect, items ...any) {
	g := u(gap)
	x := r.X
	rest := r.W
	fixed := 0
	flex := 0
	for i := 1; i < len(items); i += 2 {
		if w := items[i].(int); w > 0 {
			fixed += u(w) + g
		} else {
			flex++
		}
	}
	if flex > 0 {
		rest = max((r.W-fixed)/flex-g, u(40))
	}
	for i := 0; i < len(items); i += 2 {
		w := u(items[i+1].(int))
		if w == 0 {
			w = rest
		}
		items[i].(toolkit.Widget).SetBounds(rect{X: x, Y: r.Y, W: w, H: r.H})
		x += w + g
	}
}

// content is the area right of the navigation, above the footer.
func (s *Scene) content() rect {
	g := u(gap)
	return rect{X: u(sideW) + g, Y: u(headerH) + g, W: s.w - u(sideW) - 2*g, H: s.h - u(headerH) - u(footerH) - u(statusH) - 3*g}
}

func (s *Scene) layout() {
	if s.w == 0 {
		return
	}
	g := u(gap)
	row(rect{X: g, Y: g, W: s.w - 2*g, H: u(headerH) - 2*g}, s.title, 300, s.version, 0, s.themeBtn, 110)
	c := s.content()
	s.nav.SetBounds(rect{X: g, Y: c.Y, W: u(sideW) - g, H: c.H})
	foot := s.h - u(footerH) - u(statusH) - g
	row(rect{X: g, Y: foot, W: s.w - 2*g, H: u(footerH)}, s.hint, 0, s.errLabel, 0)
	s.status.SetBounds(rect{X: 0, Y: s.h - u(statusH), W: s.w, H: u(statusH)})
	bar := rect{X: c.X, Y: c.Y, W: c.W, H: u(toolbarH)}
	body := rect{X: c.X, Y: c.Y + u(toolbarH) + g, W: c.W, H: c.H - u(toolbarH) - g}
	line, field := u(22), u(26)
	switch s.app.View.Get() {
	case vm.ViewContainers:
		row(bar, s.cSearch, 220, s.cAll, 140, s.cStart, 80, s.cStop, 80, s.cRestart, 90, s.cRemove, 90)
		th := body.H * 45 / 100
		s.cTable.SetBounds(rect{X: body.X, Y: body.Y, W: body.W, H: th})
		dy := body.Y + th + g
		row(rect{X: body.X, Y: dy, W: body.W, H: line}, s.dTitle, 0, s.dState, 260, s.dFollow, 90)
		s.detail.SetBounds(rect{X: body.X, Y: dy + line + g, W: body.W, H: body.Y + body.H - dy - line - g})
	case vm.ViewImages:
		row(bar, s.iSearch, 220, s.iPull, 80, s.iRun, 80, s.iRemove, 90, s.iForce, 80)
		th := body.H
		if s.panel != panelNone {
			th = body.H * 40 / 100
		}
		s.iTable.SetBounds(rect{X: body.X, Y: body.Y, W: body.W, H: th})
		p := rect{X: body.X, Y: body.Y + th + g, W: body.W, H: body.H - th - g}
		row(rect{X: p.X, Y: p.Y, W: p.W, H: field}, s.panelTitle, 0, s.panelClose, 80)
		y := p.Y + field + g
		switch s.panel {
		case panelPull:
			row(rect{X: p.X, Y: y, W: p.W, H: field}, s.pullRef, 0, s.pullGo, 80)
			s.pullLog.SetBounds(rect{X: p.X, Y: y + field + g, W: p.W, H: p.Y + p.H - y - field - g})
		case panelRun:
			for i, e := range s.runFields {
				row(rect{X: p.X, Y: y, W: p.W / 2, H: u(24)}, s.runLabels[i], 100, e, 0)
				y += u(28)
			}
			row(rect{X: p.X, Y: y, W: p.W / 2, H: u(24)}, s.runRm, 0, s.runGo, 80)
			s.runLine.SetBounds(rect{X: p.X + p.W/2 + g, Y: p.Y + field + g, W: p.W/2 - g, H: line})
			s.runErr.SetBounds(rect{X: p.X + p.W/2 + g, Y: p.Y + field + 2*g + line, W: p.W/2 - g, H: line})
		}
	case vm.ViewVolumes:
		row(bar, s.vName, 200, s.vCreate, 80, s.vRemove, 90, s.vForce, 80, s.vPrune, 80, s.vAll, 120, s.vNotice, 0)
		s.vMsg.SetBounds(rect{X: body.X, Y: body.Y, W: body.W, H: line})
		th := (body.H - line - g) * 55 / 100
		s.vTable.SetBounds(rect{X: body.X, Y: body.Y + line + g, W: body.W, H: th})
		iy := body.Y + line + 2*g + th
		s.vInspect.SetBounds(rect{X: body.X, Y: iy, W: body.W, H: body.Y + body.H - iy})
	case vm.ViewTerminal:
		s.term.SetBounds(c)
	}
}

// widgets are the interactive widgets of what is shown, in focus order.
func (s *Scene) widgets() []toolkit.Widget {
	out := []toolkit.Widget{s.nav}
	switch s.app.View.Get() {
	case vm.ViewContainers:
		out = append(out, s.cSearch, s.cAll, s.cStart, s.cStop, s.cRestart, s.cRemove, s.cTable, s.dFollow, s.detail)
	case vm.ViewImages:
		out = append(out, s.iSearch, s.iPull, s.iRun, s.iRemove, s.iForce, s.iTable)
		switch s.panel {
		case panelPull:
			out = append(out, s.pullRef, s.pullGo, s.pullLog, s.panelClose)
		case panelRun:
			for _, e := range s.runFields {
				out = append(out, e)
			}
			out = append(out, s.runRm, s.runGo, s.panelClose)
		}
	case vm.ViewVolumes:
		out = append(out, s.vName, s.vCreate, s.vRemove, s.vForce, s.vPrune, s.vAll, s.vTable, s.vInspect)
	case vm.ViewTerminal:
		out = append(out, s.term)
	}
	return append(out, s.themeBtn)
}

// visible are the display-only widgets drawn with the interactive ones.
func (s *Scene) visible() []toolkit.Widget {
	out := []toolkit.Widget{s.title, s.version, s.hint, s.errLabel, s.status}
	switch s.app.View.Get() {
	case vm.ViewContainers:
		out = append(out, s.dTitle, s.dState)
	case vm.ViewImages:
		if s.panel != panelNone {
			out = append(out, s.panelTitle)
		}
		if s.panel == panelRun {
			for _, l := range s.runLabels {
				out = append(out, l)
			}
			out = append(out, s.runLine, s.runErr)
		}
	case vm.ViewVolumes:
		out = append(out, s.vMsg, s.vNotice)
	}
	return out
}

// Draw implements webcanvas.App: it first runs the work the viewmodels
// posted (results from the engine), then paints the whole scene.
func (s *Scene) Draw(buf []byte) {
	s.q.Drain()
	s.dirty = false
	p := painter.NewPixelPainter(buf, s.w, s.h)
	bg := s.theme.Background
	for i := 0; i+3 < len(buf); i += 4 {
		buf[i], buf[i+1], buf[i+2], buf[i+3] = bg.R, bg.G, bg.B, bg.A
	}
	// The sidebar: its own ground, set off from the content by a Border line.
	side := rect{X: 0, Y: u(headerH), W: u(sideW), H: s.h - u(headerH) - u(statusH)}
	p.FillRect(side, sidebarGround(s.theme))
	navTheme := *s.theme
	navTheme.Surface = sidebarGround(s.theme)
	for _, w := range s.visible() {
		w.Draw(p, s.theme)
	}
	for _, w := range s.widgets() {
		th := s.theme
		switch {
		case w == toolkit.Widget(s.term):
			th = termTheme(s.theme)
		case w == toolkit.Widget(s.detail) && s.detail.Active().Get() == 0:
			th = termTheme(s.theme)
		case w == toolkit.Widget(s.nav):
			th = &navTheme
		}
		w.Draw(p, th)
	}
	p.FillRect(rect{X: side.X + side.W - 1, Y: side.Y, W: 1, H: side.H}, s.theme.Border)
	p.FillRect(rect{X: 0, Y: side.Y - 1, W: s.w, H: 1}, s.theme.Border)
}

// Dirty reports whether something changed since the last Draw.
func (s *Scene) Dirty() bool { return s.dirty || s.q.Len() > 0 }

func (s *Scene) hit(x, y int) toolkit.Widget {
	for _, w := range s.widgets() {
		if w.HitTest(x, y) {
			return w
		}
	}
	return nil
}

func local(ev toolkit.Event, w toolkit.Widget) toolkit.Event {
	b := w.Bounds()
	ev.X -= b.X
	ev.Y -= b.Y
	return ev
}

func (s *Scene) event(kind toolkit.EventKind, x, y int) toolkit.Event {
	return toolkit.Event{Kind: kind, X: x, Y: y, Ctrl: s.mods.Ctrl, Shift: s.mods.Shift, Alt: s.mods.Alt, Meta: s.mods.Meta}
}

func (s *Scene) setFocus(w toolkit.Widget) {
	if f, ok := s.focus.(toolkit.Focusable); ok {
		f.SetFocused(false)
	}
	s.focus = w
	if f, ok := w.(toolkit.Focusable); ok {
		f.SetFocused(true)
	}
}

// Click implements webcanvas.App.
func (s *Scene) Click(x, y int) bool {
	w := s.hit(x, y)
	if w == nil {
		return false
	}
	s.setFocus(w)
	s.drag, s.dragBounds = w, w.Bounds()
	w.OnEvent(local(s.event(toolkit.EventClick, x, y), w))
	return true
}

// Move implements webcanvas.App: a drag while a button is held, else hover
// (which shows a hovered action's command line).
func (s *Scene) Move(x, y int) bool {
	if s.drag != nil {
		s.drag.OnEvent(local(s.event(toolkit.EventMouseDrag, x, y), s.drag))
		return true
	}
	b, _ := s.hit(x, y).(*toolkit.Button)
	if b == s.hover {
		return false
	}
	s.hover = b
	if b != nil {
		if line := s.buttonLine(b); line != "" {
			s.hint.Text().Set(line)
		}
	}
	return true
}

// Release implements webcanvas.App.
func (s *Scene) Release(x, y int) bool {
	if s.drag == nil {
		return false
	}
	s.drag.OnEvent(local(s.event(toolkit.EventMouseUp, x, y), s.drag))
	s.drag = nil
	return true
}

// Context implements webcanvas.App: a right click selects like a click.
func (s *Scene) Context(x, y int) bool { return s.Click(x, y) }

// Scroll implements webcanvas.Scroller.
func (s *Scene) Scroll(x, y, dx, dy int) bool {
	w := s.hit(x, y)
	if w == nil {
		return false
	}
	ev := local(s.event(toolkit.EventScroll, x, y), w)
	ev.Delta, ev.DeltaX = dy, dx
	w.OnEvent(ev)
	return true
}

// Char implements webcanvas.App.
func (s *Scene) Char(c string) bool {
	if s.focus == nil {
		return false
	}
	s.focus.OnEvent(toolkit.Event{Kind: toolkit.EventChar, Code: c, Alt: s.mods.Alt})
	return true
}

// KeyDown implements webcanvas.App. The terminal, when it has focus, gets
// every key it claims (Ctrl-C/D/L/R/W/U, arrows, Tab, ...) before any
// global shortcut; the shortcuts are F6 (next region), Tab / Shift-Tab
// (next / previous control) and Alt+1..4 (views).
func (s *Scene) KeyDown(key string) bool {
	ev := toolkit.Event{Kind: toolkit.EventKeyDown, Code: key, Ctrl: s.mods.Ctrl, Shift: s.mods.Shift, Alt: s.mods.Alt, Meta: s.mods.Meta}
	if t, ok := s.focus.(*toolkit.Terminal); ok && t.ClaimsKey(ev) {
		t.OnEvent(ev)
		return true
	}
	switch {
	case key == "F6":
		s.cycleFocus(!ev.Shift, true)
		return true
	case key == "Tab":
		s.cycleFocus(!ev.Shift, false)
		return true
	case ev.Alt && len(key) == 1 && key[0] >= '1' && key[0] < '1'+vm.NumViews:
		s.app.View.Set(int(key[0] - '1'))
		return true
	}
	if s.focus == nil {
		return false
	}
	s.focus.OnEvent(ev)
	return true
}

// cycleFocus moves the focus to the next (or previous) control; by region
// jumps between the navigation and the view's main widget.
func (s *Scene) cycleFocus(forward, region bool) {
	ws := s.widgets()
	if region {
		ws = []toolkit.Widget{s.nav, s.main()}
	}
	i := -1
	for k, w := range ws {
		if w == s.focus {
			i = k
		}
	}
	step := 1
	if !forward {
		step = len(ws) - 1
	}
	s.setFocus(ws[(i+step+len(ws))%len(ws)])
}

// main is the view's principal widget (where F6 lands).
func (s *Scene) main() toolkit.Widget {
	switch s.app.View.Get() {
	case vm.ViewContainers:
		return s.cTable
	case vm.ViewImages:
		return s.iTable
	case vm.ViewVolumes:
		return s.vTable
	}
	return s.term
}
