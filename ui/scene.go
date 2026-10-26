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
	"github.com/go-widgets/toolkit"
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

// monoFont is the terminals' face; nil when it cannot be built (the
// toolkit's default then serves).
var monoFont = func() toolkit.Font {
	f, err := toolkit.NewTrueTypeFont(jetbrainsmono.TTF, 14)
	if err != nil {
		return nil
	}
	return f
}()

// New builds the scene over app, sized w x h, posting UI work through q.
func New(app *vm.App, q *mvvm.Queue, w, h int) *Scene {
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

func (s *Scene) invalidate() {
	s.dirty = true
	if s.repaint != nil {
		s.repaint()
	}
}

func button(label string, onClick func()) *toolkit.Button { return toolkit.NewButton(label, onClick) }

func (s *Scene) build() {
	inv := s.invalidate
	a := s.app

	s.title = toolkit.NewLabel("go-wasmcontainers · dashboard")
	s.version = toolkit.NewLabel("")
	bindLabel(s.version, func() string { return a.EngineVersion.Get() }, inv, a.EngineVersion)
	s.themeBtn = button("Dark theme", s.toggleTheme)

	s.nav = toolkit.NewListBox(vm.ViewNames[:])
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
	bindObs(s.cSearch.Text(), c.Search, inv)
	s.cAll = toolkit.NewCheckButton("Show stopped", true)
	bindObs(s.cAll.Checked(), c.ShowAll, inv)
	s.cStart = button("Start", func() { s.withContainer(c.Start) })
	s.cStop = button("Stop", func() { s.withContainer(c.Stop) })
	s.cRestart = button("Restart", func() { s.withContainer(c.Restart) })
	s.cRemove = button("Remove", func() { s.withContainer(c.Remove) })
	cols := make([]toolkit.TableColumn, vm.NumContainerCols)
	for i, t := range vm.ContainerColumns {
		cols[i] = toolkit.TableColumn{Title: t, Sortable: true}
	}
	s.cTable = toolkit.NewTable(cols, nil)
	s.cTable.MultiSelect = true // a click selects (one row: the actions take one)
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
	bindLabel(s.dState, d.State.Get, inv, d.State)
	s.dFollow = toolkit.NewCheckButton("Follow", true)
	bindObs(s.dFollow.Checked(), d.Follow, inv)
	s.logs = toolkit.NewTerminal(80, 10, 5000)
	s.logs.Font = monoFont
	s.logs.Screen().SetNewlineMode(true)
	d.LogOut = func(b []byte) { s.logs.Write(b); inv() }
	d.LogReset = func() {
		s.logs.Screen().WriteString("\x1bc")
		s.logs.Screen().ClearScrollback()
		inv()
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
	s.stats = newStatsPanel(d, inv)
	s.detail.AddTab("Stats", s.stats)

	// Images.
	im := a.Images
	s.iSearch = toolkit.NewSearchEntry("")
	bindObs(s.iSearch.Text(), im.Search, inv)
	s.iPull = button("Pull…", func() { s.openPanel(panelPull) })
	s.iRun = button("Run…", func() {
		if r, ok := im.Find(im.Selected.Get()); ok {
			im.Run.Reset(r.Ref)
		}
		s.openPanel(panelRun)
	})
	s.iForce = toolkit.NewCheckButton("Force", false)
	s.iRemove = button("Remove", func() {
		if r, ok := im.Find(im.Selected.Get()); ok {
			im.Remove(r, s.iForce.Checked().Get())
		}
	})
	icols := make([]toolkit.TableColumn, vm.NumImageCols)
	for i, t := range vm.ImageColumns {
		icols[i] = toolkit.TableColumn{Title: t}
	}
	s.iTable = toolkit.NewTable(icols, nil)
	s.iTable.MultiSelect = true
	bindRows(s.iTable, im.Rows, vm.ImageRow.Cells, func() { s.syncImageSelection(); inv() })
	s.iTable.Selected().Subscribe(func(i int) {
		if i >= 0 && i < im.Rows.Len() {
			im.Selected.Set(im.Rows.At(i).Key())
		}
	})
	im.Selected.Subscribe(func(string) { s.syncImageSelection(); s.updateButtons(); inv() })
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
	bindRows(s.vTable, v.Rows, vm.VolumeRow.Cells, func() { s.syncVolumeSelection(); inv() })
	s.vTable.Selected().Subscribe(func(i int) {
		if i >= 0 && i < v.Rows.Len() {
			v.Selected.Set(v.Rows.At(i).Name)
		}
	})
	v.Selected.Subscribe(func(string) { s.syncVolumeSelection(); s.updateButtons(); inv() })
	s.vMsg = toolkit.NewLabel("")
	bindLabel(s.vMsg, v.Message.Get, inv, v.Message)
	s.vNotice = toolkit.NewLabel("")
	bindLabel(s.vNotice, v.Notice.Get, inv, v.Notice)
	s.vName = toolkit.NewEntry("")
	s.vName.Placeholder = "new volume name"
	bindObs(s.vName.Text(), v.NewName, inv)
	s.vCreate = button("Create", v.Create)
	s.vForce = toolkit.NewCheckButton("Force", false)
	s.vRemove = button("Remove", func() {
		if name := v.Selected.Get(); name != "" {
			v.Remove(name, s.vForce.Checked().Get())
		}
	})
	s.vAll = toolkit.NewCheckButton("All unused", false)
	s.vPrune = button("Prune", func() { v.Prune(s.vAll.Checked().Get()) })
	s.vInspect = toolkit.NewListBox(nil)
	bindItems(s.vInspect, v.Inspect, inspectLine, inv)

	// Terminal.
	s.term = toolkit.NewTerminal(80, 24, 5000)
	s.term.Font = monoFont
	s.term.OnInput = a.Terminal.Input
	s.term.OnResize = func(cols, _ int) { a.Terminal.Resize(cols) }
	a.Terminal.Attach(func(out string) { s.term.Write([]byte(out)); inv() }, func() {
		s.term.Screen().WriteString("\x1b[H\x1b[2J")
		s.term.Screen().ClearScrollback()
		inv()
	})

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
		set := func() { s.status.SetSegment(i, o.Get()); inv() }
		set()
		o.SubscribeChanged(set)
	}
	s.updateButtons()
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
func bindObs[T any](widget, model *mvvm.Observable[T], invalidate func()) {
	widget.Set(model.Get())
	model.Subscribe(func(v T) { widget.Set(v); invalidate() })
	widget.Subscribe(func(v T) { model.Set(v) })
}

func (s *Scene) withContainer(act func(vm.ContainerRow)) {
	if r, ok := s.app.Containers.Find(s.app.Containers.Selected.Get()); ok {
		act(r)
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
	r, ok := s.app.Containers.Find(s.app.Containers.Selected.Get())
	s.cStart.Disabled().Set(!ok || r.Running())
	s.cStop.Disabled().Set(!ok || !r.Running())
	s.cRestart.Disabled().Set(!ok)
	s.cRemove.Disabled().Set(!ok)
	_, img := s.app.Images.Find(s.app.Images.Selected.Get())
	s.iRemove.Disabled().Set(!img)
	s.vRemove.Disabled().Set(s.app.Volumes.Selected.Get() == "")
}

// buttonLine is the command line a hovered action button stands for.
func (s *Scene) buttonLine(b *toolkit.Button) string {
	c := s.app.Containers
	r, ok := c.Find(c.Selected.Get())
	switch {
	case b == s.cStart && ok:
		return "Start = " + c.StartLine(r)
	case b == s.cStop && ok:
		return "Stop = " + c.StopLine(r)
	case b == s.cRestart && ok:
		return "Restart = " + c.RestartLine(r)
	case b == s.cRemove && ok:
		return "Remove = " + c.RemoveLine(r)
	}
	im := s.app.Images
	if ir, ok := im.Find(im.Selected.Get()); ok && b == s.iRemove {
		return "Remove = " + im.RemoveLine(ir, s.iForce.Checked().Get())
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
	case b == s.vRemove && v.Selected.Get() != "":
		return "Remove = " + v.RemoveLine(v.Selected.Get(), s.vForce.Checked().Get())
	case b == s.vPrune:
		return "Prune = " + v.PruneLine(s.vAll.Checked().Get())
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
