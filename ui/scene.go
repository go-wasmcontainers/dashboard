// Package ui is the dashboard's view: go-widgets bound to the viewmodels
// of package vm. It implements go-widgets/webcanvas's App (and its Resizer,
// Scroller, RepaintAware and ModifierAware companions), so the same scene
// runs in a browser canvas and in a native test against a pixel buffer.
//
// The view holds no state of its own beyond widget placement and focus:
// what it shows comes from the viewmodels' observables, what the user does
// goes to the viewmodels' actions.
package ui

import (
	"strings"

	"github.com/go-opentype/fonts/jetbrainsmono"
	"github.com/go-widgets/mvvm"
	"github.com/go-widgets/painter"
	"github.com/go-widgets/shellterm"
	"github.com/go-widgets/toolkit"
	"github.com/go-widgets/toolkit/iconoir"
	"github.com/go-widgets/webcanvas"

	"github.com/go-wasmcontainers/dashboard/vm"
)

// Layout metrics (pixels).
const (
	headerH  = 40
	sideW    = 150
	toolbarH = 30
	gap      = 8
	footerH  = 24
	statusH  = 22
	minW     = 640
	minH     = 420
)

// Scene is the dashboard's widget tree.
type Scene struct {
	app   *vm.App
	q     *mvvm.Queue
	w, h  int
	theme *toolkit.Theme
	dark  bool
	mods  webcanvas.Modifiers

	repaint func()
	dirty   bool

	// What the next frame must repaint (see DrawDamaged). full: all of it (a
	// layout, a theme, a view, a selection: anything that is not one widget's
	// content). unscoped: an input event changed something nobody said where;
	// the frame repaints all of it and reports what differs. dmg: the places
	// widgets reported (invalidate with widgets). With none of the three the
	// scene is as it was, and the frame has no scene to repaint.
	full, unscoped bool
	dmg            []rect
	// prev is the framebuffer before a whole repaint, to find what it changed.
	prev []byte

	title, version *toolkit.Label
	themeBtn       *toolkit.Button
	nav            *toolkit.ListBox

	// containers view
	cSearch                          *toolkit.SearchEntry
	cAll                             *toolkit.CheckButton
	cStart, cStop, cRestart, cRemove *toolkit.Button
	cTable                           *toolkit.Table
	dTitle, dState                   *toolkit.Label
	dFollow                          *toolkit.CheckButton
	detail                           *toolkit.Notebook
	logs                             *toolkit.Terminal
	inspect, env, mounts             *toolkit.ListBox
	// the shell inside the shown container: the Open shell action and the
	// detail's Shell tab
	cShell    *toolkit.Button
	shellTerm *toolkit.Terminal

	// images view
	iSearch                *toolkit.SearchEntry
	iPull, iRun, iRemove   *toolkit.Button
	iForce                 *toolkit.CheckButton
	iTable                 *toolkit.Table
	panel                  int // panelNone, panelPull, panelRun
	pullRef                *toolkit.Entry
	pullGo                 *toolkit.Button
	pullLog                *toolkit.ListBox
	runFields              []*toolkit.Entry
	runLabels              []*toolkit.Label
	runRm                  *toolkit.CheckButton
	runGo                  *toolkit.Button
	runLine, runErr        *toolkit.Label
	panelTitle, panelClose *toolkit.Button

	// volumes view
	vTable                   *toolkit.Table
	vMsg, vNotice            *toolkit.Label
	vName                    *toolkit.Entry
	vCreate, vRemove, vPrune *toolkit.Button
	vForce, vAll             *toolkit.CheckButton
	vInspect                 *toolkit.ListBox

	// networks view, and the class status and "Join class" dialog on it
	nTable                *toolkit.Table
	nMsg, classInfo       *toolkit.Label
	nName                 *toolkit.Entry
	nCreate, nRemove      *toolkit.Button
	classBtn              *toolkit.Button
	nInspect              *toolkit.ListBox
	jTitle, jRelayShown   *toolkit.Label
	jErr, jNote           *toolkit.Label
	jRelay, jName, jCode  *toolkit.Entry
	jOK, jChange, jCancel *toolkit.Button
	jRelayLbl, jNameLbl   *toolkit.Label
	jCodeLbl              *toolkit.Label

	// the detail's Networks tab
	dNets *toolkit.ListBox

	// surfaces view: one pane per GPU surface, frames drawn as layers
	sEmpty        *toolkit.Label
	panes         []*pane
	surfaceCanvas SurfaceCanvas
	// paneFocus is the pane whose guest gets the keys (nil: the widgets
	// have the focus); code is the physical key of the event being
	// delivered (webcanvas.KeyCodeAware); paneDrag the pane a press began on.
	paneFocus *pane
	paneDrag  *pane
	// locked: the browser has the pointer locked to the canvas for paneFocus
	// (SetPointerLocked); its absolute moves are then not the guest's.
	locked bool
	code   string

	// the detail's Stats tab
	stats *statsPanel

	// terminal view
	term *toolkit.Terminal

	// footer
	hint, errLabel *toolkit.Label
	status         *toolkit.Statusbar

	focus      toolkit.Widget
	drag       toolkit.Widget
	dragBounds toolkit.Rect
	hover      *toolkit.Button
}

// Panels of the images view.
const (
	panelNone = iota
	panelPull
	panelRun
)

// monoPx is the terminals' text size in logical pixels.
const monoPx = 14

// monoFace is the terminals' face (JetBrains Mono, anti-aliased) at monoPx ×
// ratio device pixels; nil when it cannot be built (the toolkit's default
// then serves).
func monoFace(ratio float64) toolkit.Font {
	f, err := toolkit.NewTrueTypeFont(jetbrainsmono.TTF, max(int(monoPx*ratio+0.5), 1))
	if err != nil {
		return nil
	}
	return f
}

var monoFont = monoFace(1)

// New builds the scene over app, sized w x h, posting UI work through q.
// Text is the toolkit's anti-aliased OpenType face (Atkinson Hyperlegible),
// not its 5x7 bitmap default.
func New(app *vm.App, q *mvvm.Queue, w, h int) *Scene {
	_ = toolkit.UseOpenTypeText() // on the impossible error, the bitmap face stays
	s := &Scene{app: app, q: q, theme: Light()}
	s.build()
	s.Resize(w, h)
	return s
}

// Wake asks for a frame (safe from any goroutine): the hosts call it when
// the viewmodels post work, which the next Draw runs.
func (s *Scene) Wake() {
	if s.repaint != nil {
		s.repaint()
	}
}

// invalidate asks for a frame. With no widget the whole scene is repainted:
// that is the safe answer for a change that is not one widget's content. With
// widgets, only the places they are drawn are, which is what a label's text, a
// list's items or a terminal's output cost. A widget's place is its bounds plus
// damageMargin, for the focus ring and the anti-aliased edge that leave them.
func (s *Scene) invalidate(ws ...toolkit.Widget) {
	s.dirty = true
	if len(ws) == 0 {
		s.full = true
	}
	for _, w := range ws {
		b := w.Bounds()
		m := u(damageMargin)
		s.dmg = append(s.dmg, rect{X: b.X - m, Y: b.Y - m, W: b.W + 2*m, H: b.H + 2*m})
	}
	if s.repaint != nil {
		s.repaint()
	}
}

// damageMargin is how far past its bounds a widget's damage reaches, in
// logical pixels.
const damageMargin = 2

func button(label string, onClick func()) *toolkit.Button { return toolkit.NewButton(label, onClick) }

func (s *Scene) build() {
	inv := s.invalidate
	a := s.app

	s.title = toolkit.NewLabel("go-wasmcontainers · dashboard")
	s.version = toolkit.NewLabel("")
	bindLabel(s.version, func() string { return a.EngineVersion.Get() }, inv, a.EngineVersion)
	s.themeBtn = button("Dark theme", s.toggleTheme)

	s.nav = toolkit.NewListBox(vm.ViewNames[:])
	s.nav.RowHeight = u(navRowH)
	s.nav.ItemRenderer = navItem
	s.nav.Selected().Set(a.View.Get())
	s.nav.Selected().Subscribe(func(i int) {
		if i >= 0 && i < vm.NumViews {
			a.View.Set(i)
		}
	})
	a.View.Subscribe(func(v int) {
		s.nav.Selected().Set(v)
		s.layout()
		inv()
	})

	// Containers.
	c := a.Containers
	s.cSearch = toolkit.NewSearchEntry("")
	s.cSearch.Icon = searchIcon
	bindObs(s.cSearch.Text(), c.Search, inv)
	s.cAll = toolkit.NewCheckButton("Show stopped", true)
	bindObs(s.cAll.Checked(), c.ShowAll, inv)
	s.cStart = button("Start", c.StartSelected)
	s.cStop = button("Stop", c.StopSelected)
	s.cRestart = button("Restart", c.RestartSelected)
	s.cRemove = button("Remove", c.RemoveSelected)
	s.cShell = button("Open shell", func() { s.withContainer(s.openShell) })
	cols := make([]toolkit.TableColumn, vm.NumContainerCols)
	for i, t := range vm.ContainerColumns {
		cols[i] = toolkit.TableColumn{Title: t, Sortable: true}
	}
	s.cTable = toolkit.NewTable(cols, nil)
	s.cTable.MultiSelect = true // the actions take every selected row (Open shell: one)
	s.cTable.Framed, s.cTable.RowLines = true, true
	bindObs(s.cTable.SortColumn(), c.SortCol, inv)
	bindObs(s.cTable.SortAsc(), c.SortAsc, inv)
	s.cTable.CellFill = func(row, col int) (toolkit.RGBA, bool) {
		if col != vm.ColState || row >= c.Rows.Len() {
			return toolkit.RGBA{}, false
		}
		r := c.Rows.At(row)
		code, exited := r.ExitCode()
		return StateColor(r.State, code, exited)
	}
	bindRows(s.cTable, c.Rows, vm.ContainerRow.Cells, func() { s.syncContainerSelection(); inv() })
	s.cTable.Selected().Subscribe(func(i int) {
		if i >= 0 && i < c.Rows.Len() {
			c.Selected.Set(c.Rows.At(i).ID)
		}
	})
	c.SelectedSet.SubscribeChanged(func() { s.updateButtons(); inv() })
	c.Selected.Subscribe(func(id string) {
		a.Detail.Open(id)
		s.syncContainerSelection()
		s.updateButtons()
		inv()
	})

	d := a.Detail
	s.dTitle = toolkit.NewLabel("")
	bindLabel(s.dTitle, func() string {
		if d.ID.Get() == "" {
			return "Select a container to see its logs, inspect tree, environment and mounts."
		}
		return d.Title.Get()
	}, inv, d.ID, d.Title)
	s.dState = toolkit.NewLabel("")
	// The header's right-hand side: the container's state, or, on the
	// Shell tab, the shell's command line and how it goes. (Bound below,
	// once the notebook exists.)
	s.dFollow = toolkit.NewCheckButton("Follow", true)
	bindObs(s.dFollow.Checked(), d.Follow, inv)
	s.logs = toolkit.NewTerminal(80, 10, 5000)
	s.logs.Font = monoFont
	s.logs.Screen().SetNewlineMode(true)
	d.LogOut = func(b []byte) { s.logs.Write(b); inv(s.logs) }
	d.LogReset = func() {
		s.logs.Screen().WriteString("\x1bc")
		s.logs.Screen().ClearScrollback()
		inv(s.logs)
	}
	s.inspect = toolkit.NewListBox(nil)
	bindItems(s.inspect, d.Tree, inspectLine, inv)
	s.env = toolkit.NewListBox(nil)
	bindItems(s.env, d.Env, func(e string) string { return e }, inv)
	s.mounts = toolkit.NewListBox(nil)
	bindItems(s.mounts, d.Mounts, func(m string) string { return m }, inv)
	s.detail = toolkit.NewNotebook()
	s.detail.AddTab("Logs", s.logs)
	s.detail.AddTab("Inspect", s.inspect)
	s.detail.AddTab("Environment", s.env)
	s.detail.AddTab("Mounts", s.mounts)
	s.dNets = toolkit.NewListBox(nil)
	bindItems(s.dNets, d.Networks, func(n string) string { return n }, inv)
	s.detail.AddTab("Networks", s.dNets)
	s.stats = newStatsPanel(d, inv)
	s.detail.AddTab("Stats", s.stats)
	s.buildShell()

	// Images.
	im := a.Images
	s.iSearch = toolkit.NewSearchEntry("")
	s.iSearch.Icon = searchIcon
	bindObs(s.iSearch.Text(), im.Search, inv)
	s.iPull = button("Pull…", func() { s.openPanel(panelPull) })
	s.iRun = button("Run…", func() {
		if r, ok := im.Find(im.Selected.Get()); ok {
			im.Run.Reset(r.Ref)
		}
		s.openPanel(panelRun)
	})
	s.iForce = toolkit.NewCheckButton("Force", false)
	s.iRemove = button("Remove", func() { im.RemoveSelected(s.iForce.Checked().Get()) })
	icols := make([]toolkit.TableColumn, vm.NumImageCols)
	for i, t := range vm.ImageColumns {
		icols[i] = toolkit.TableColumn{Title: t}
	}
	s.iTable = toolkit.NewTable(icols, nil)
	s.iTable.MultiSelect = true
	s.iTable.Framed, s.iTable.RowLines = true, true
	bindRows(s.iTable, im.Rows, vm.ImageRow.Cells, func() { s.syncImageSelection(); inv() })
	s.iTable.Selected().Subscribe(func(i int) {
		if i >= 0 && i < im.Rows.Len() {
			im.Selected.Set(im.Rows.At(i).Key())
		}
	})
	im.Selected.Subscribe(func(string) { s.syncImageSelection(); s.updateButtons(); inv() })
	im.SelectedSet.SubscribeChanged(func() { s.updateButtons(); inv() })
	s.panelTitle = button("", nil)
	s.panelClose = button("Close", func() { s.openPanel(panelNone) })
	s.pullRef = toolkit.NewEntry("")
	s.pullRef.Placeholder = "image reference, e.g. hello or clock"
	bindObs(s.pullRef.Text(), im.PullRef, inv)
	s.pullGo = button("Pull", im.Pull)
	s.pullLog = toolkit.NewListBox(nil)
	bindItems(s.pullLog, im.PullLog, func(l string) string { return l }, inv)
	f := im.Run
	for _, fld := range []struct {
		label, hint string
		obs         *mvvm.Observable[string]
	}{
		{"Image", "hello", f.Image}, {"Name", "optional", f.Name}, {"Command", "arguments after the image", f.Command},
		{"Environment", "KEY=value, comma separated", f.Env}, {"Entrypoint", "optional", f.Entrypoint},
		{"Working dir", "optional", f.Workdir}, {"Memory", "e.g. 64m", f.Memory},
		{"Volumes", "NAME:/path[:ro], comma separated", f.Volumes},
	} {
		e := toolkit.NewEntry("")
		e.Placeholder = fld.hint
		bindObs(e.Text(), fld.obs, inv)
		s.runFields = append(s.runFields, e)
		s.runLabels = append(s.runLabels, toolkit.NewLabel(fld.label))
	}
	s.runRm = toolkit.NewCheckButton("Remove when it exits (--rm)", false)
	bindObs(s.runRm.Checked(), f.AutoRemove, inv)
	s.runGo = button("Run", func() {
		if f.Start() {
			s.openPanel(panelNone)
			a.View.Set(vm.ViewContainers)
		}
	})
	s.runLine = toolkit.NewLabel("")
	bindLabel(s.runLine, func() string { return "= " + f.Line.Get() }, inv, f.Line)
	s.runErr = toolkit.NewLabel("")
	bindLabel(s.runErr, f.Error.Get, inv, f.Error)

	// Volumes.
	vcols := make([]toolkit.TableColumn, len(vm.VolumeColumns))
	for i, t := range vm.VolumeColumns {
		vcols[i] = toolkit.TableColumn{Title: t}
	}
	v := a.Volumes
	s.vTable = toolkit.NewTable(vcols, nil)
	s.vTable.MultiSelect = true
	s.vTable.Framed, s.vTable.RowLines = true, true
	bindRows(s.vTable, v.Rows, vm.VolumeRow.Cells, func() { s.syncVolumeSelection(); inv() })
	s.vTable.Selected().Subscribe(func(i int) {
		if i >= 0 && i < v.Rows.Len() {
			v.Selected.Set(v.Rows.At(i).Name)
		}
	})
	v.Selected.Subscribe(func(string) { s.syncVolumeSelection(); s.updateButtons(); inv() })
	v.SelectedSet.SubscribeChanged(func() { s.updateButtons(); inv() })
	s.vMsg = toolkit.NewLabel("")
	bindLabel(s.vMsg, v.Message.Get, inv, v.Message)
	s.vNotice = toolkit.NewLabel("")
	bindLabel(s.vNotice, v.Notice.Get, inv, v.Notice)
	s.vName = toolkit.NewEntry("")
	s.vName.Placeholder = "new volume name"
	bindObs(s.vName.Text(), v.NewName, inv)
	s.vCreate = button("Create", v.Create)
	s.vForce = toolkit.NewCheckButton("Force", false)
	s.vRemove = button("Remove", func() { v.RemoveSelected(s.vForce.Checked().Get()) })
	s.vAll = toolkit.NewCheckButton("All unused", false)
	s.vPrune = button("Prune", func() { v.Prune(s.vAll.Checked().Get()) })
	s.vInspect = toolkit.NewListBox(nil)
	bindItems(s.vInspect, v.Inspect, inspectLine, inv)

	s.buildNetworks()
	s.buildSurfaces()

	// Terminal.
	s.term = toolkit.NewTerminal(80, 24, 5000)
	s.term.Font = monoFont
	// Input, resize, output and clear (screen and scrollback) between the
	// widget and the session: go-widgets/shellterm, shared with wasmdesk.
	shellterm.Bind(s.term, a.Terminal.Session, func() { inv(s.term) })

	// Footer.
	s.hint = toolkit.NewLabel("")
	bindLabel(s.hint, func() string {
		if l := a.LastCommand.Get(); l != "" {
			return "= " + l
		}
		return "Every action shows the wctr command it stands for here."
	}, inv, a.LastCommand)
	s.errLabel = toolkit.NewLabel("")
	bindLabel(s.errLabel, a.Error.Get, inv, a.Error)
	s.status = toolkit.NewStatusbar([]string{"", "", "", ""})
	for i, o := range []*mvvm.Observable[string]{a.EngineVersion, a.Summary, a.Storage, a.Live} {
		i, o := i, o
		set := func() { s.status.SetSegment(i, o.Get()); inv(s.status) }
		set()
		o.SubscribeChanged(set)
	}
	s.updateButtons()
}

// navRowH is a navigation row's height in logical pixels.
const navRowH = 32

// navIcons are the views' icons: Iconoir glyphs (MIT, go-icons/iconoir),
// drawn through the toolkit's icon seam, rasterised at the row's device
// size and recoloured to the row's ink.
var navIcons = [vm.NumViews]string{"box", "package", "database", "network", "terminal", "tv"}

// navItem draws a navigation row: its icon, then its label.
func navItem(p painter.Painter, theme *toolkit.Theme, rc toolkit.Rect, index int, item string, selected bool, ink toolkit.RGBA) {
	sz := u(18)
	pad := u(8)
	if index >= 0 && index < len(navIcons) {
		iconoir.Drawer(navIcons[index])(p, toolkit.Rect{X: rc.X + pad, Y: rc.Y + (rc.H-sz)/2, W: sz, H: sz}, ink)
	}
	toolkit.DrawText(p, rc.X+2*pad+sz, rc.Y+(rc.H-toolkit.GlyphHeight())/2, item, ink)
}

// searchIcon is the search fields' magnifier: Iconoir's "search", square and
// centred in the slot the field gives it.
func searchIcon(p painter.Painter, r toolkit.Rect, ink toolkit.RGBA) {
	sz := min(r.W, r.H, u(16))
	iconoir.Drawer("search")(p, toolkit.Rect{X: r.X + (r.W-sz)/2, Y: r.Y + (r.H-sz)/2, W: sz, H: sz}, ink)
}

// inspectLine renders an inspect tree line, indented by depth.
func inspectLine(n vm.InspectNode) string {
	text := n.Key
	if text != "" {
		text += ": "
	}
	return strings.Repeat("   ", n.Depth) + text + n.Value
}

func (s *Scene) syncVolumeSelection() {
	v := s.app.Volumes
	sel := -1
	for i := 0; i < v.Rows.Len(); i++ {
		if v.Rows.At(i).Name == v.Selected.Get() {
			sel = i
		}
	}
	s.vTable.Selected().Set(sel)
}

// bindObs mirrors a widget observable and a viewmodel one, both ways.
func bindObs[T any](widget, model *mvvm.Observable[T], invalidate invalidator) {
	widget.Set(model.Get())
	model.Subscribe(func(v T) { widget.Set(v); invalidate() })
	widget.Subscribe(func(v T) { model.Set(v) })
}

func (s *Scene) withContainer(act func(vm.ContainerRow)) {
	// A one-row action: with several rows selected it does nothing.
	if rows := s.app.Containers.Targets(); len(rows) == 1 {
		act(rows[0])
	}
}

func (s *Scene) syncContainerSelection() {
	c := s.app.Containers
	sel := -1
	for i := 0; i < c.Rows.Len(); i++ {
		if c.Rows.At(i).ID == c.Selected.Get() {
			sel = i
		}
	}
	s.cTable.Selected().Set(sel)
	s.updateButtons()
}

func (s *Scene) syncImageSelection() {
	im := s.app.Images
	sel := -1
	for i := 0; i < im.Rows.Len(); i++ {
		if im.Rows.At(i).Key() == im.Selected.Get() {
			sel = i
		}
	}
	s.iTable.Selected().Set(sel)
}

// updateButtons enables the actions that apply to the selection.
func (s *Scene) updateButtons() {
	c := s.app.Containers
	rows := c.Targets()
	s.cStart.Disabled().Set(len(c.StartJobs()) == 0)
	s.cStop.Disabled().Set(len(c.StopJobs()) == 0)
	s.cRestart.Disabled().Set(len(rows) == 0)
	s.cRemove.Disabled().Set(len(rows) == 0)
	s.cShell.Disabled().Set(len(rows) != 1 || !rows[0].Running())
	s.iRemove.Disabled().Set(len(s.app.Images.Targets()) == 0)
	s.vRemove.Disabled().Set(len(s.app.Volumes.Targets()) == 0)
	s.nRemove.Disabled().Set(len(s.app.Networks.Targets()) == 0)
}

// buttonLine is the command line a hovered action button stands for.
func (s *Scene) buttonLine(b *toolkit.Button) string {
	c := s.app.Containers
	rows := c.Targets()
	switch {
	case b == s.cStart && len(c.StartJobs()) > 0:
		return "Start = " + vm.JobsLine(c.StartJobs())
	case b == s.cStop && len(c.StopJobs()) > 0:
		return "Stop = " + vm.JobsLine(c.StopJobs())
	case b == s.cRestart && len(rows) > 0:
		return "Restart = " + vm.JobsLine(c.RestartJobs())
	case b == s.cShell && len(rows) == 1:
		return "Open shell = " + vm.Line("exec", "-it", rows[0].Ref(), "sh")
	case b == s.cRemove && len(rows) > 0:
		return "Remove = " + vm.JobsLine(c.RemoveJobs())
	}
	im := s.app.Images
	if b == s.iRemove && len(im.Targets()) > 0 {
		return "Remove = " + vm.JobsLine(im.RemoveJobs(s.iForce.Checked().Get()))
	}
	if b == s.pullGo {
		return "Pull = " + im.PullLine()
	}
	v := s.app.Volumes
	switch {
	case b == s.runGo:
		return "Run = " + im.Run.Line.Get()
	case b == s.vCreate:
		return "Create = " + v.CreateLine()
	case b == s.vRemove && len(v.Targets()) > 0:
		return "Remove = " + vm.JobsLine(v.RemoveJobs(s.vForce.Checked().Get()))
	case b == s.vPrune:
		return "Prune = " + v.PruneLine(s.vAll.Checked().Get())
	}
	n := s.app.Networks
	switch {
	case b == s.nCreate:
		return "Create = " + n.CreateLine()
	case b == s.nRemove && len(n.Targets()) > 0:
		return "Remove = " + vm.JobsLine(n.RemoveJobs())
	}
	return ""
}

func (s *Scene) openPanel(p int) {
	s.panel = p
	switch p {
	case panelPull:
		s.panelTitle.Label().Set("Pull an image")
		s.setFocus(s.pullRef)
	case panelRun:
		s.panelTitle.Label().Set("Run a container (wctr run -d)")
		s.setFocus(s.runFields[0])
	}
	s.layout()
	s.invalidate()
}

func (s *Scene) toggleTheme() {
	s.dark = !s.dark
	if s.dark {
		s.theme = Dark()
		s.themeBtn.Label().Set("Light theme")
	} else {
		s.theme = Light()
		s.themeBtn.Label().Set("Dark theme")
	}
	s.invalidate()
}
