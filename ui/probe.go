package ui

import (
	"github.com/go-widgets/toolkit"
	"github.com/go-widgets/toolkit/vt"

	"github.com/go-wasmcontainers/dashboard/vm"
)

// Box is a widget's place on the canvas, in canvas pixels.
type Box struct{ X, Y, W, H int }

// Probe is the scene's state for interaction tests (the page exposes it
// as window.__dashboard.state()): what is shown, where each control is,
// and what the terminal and the log pane hold — never pixels.
type Probe struct {
	View        string
	Focus       string
	Boxes       map[string]Box
	Containers  []string
	Images      []string
	Selected    string
	LastCommand string
	Hint        string
	Error       string
	Live        string
	Status      []string
	Pending     int
	Running     bool
	Terminal    string
	Logs        string
	Volumes     string
	VolumeNames []string
	Mounts      []string
	Memory      float64
	CPU         string
	Dark        bool
}

func screenText(s *vt.Screen) string {
	cols, rows := s.Size()
	first := s.ScrollbackLen()
	return s.Text(vt.Pos{Line: first}, vt.Pos{Line: first + rows - 1, Col: cols - 1})
}

// named are the controls a test can address.
func (s *Scene) named() map[string]toolkit.Widget {
	m := map[string]toolkit.Widget{
		"nav": s.nav, "theme": s.themeBtn,
		"containers.search": s.cSearch, "containers.all": s.cAll, "containers.start": s.cStart,
		"containers.stop": s.cStop, "containers.restart": s.cRestart, "containers.remove": s.cRemove,
		"containers.table": s.cTable, "detail": s.detail, "detail.follow": s.dFollow,
		"images.search": s.iSearch, "images.pull": s.iPull, "images.run": s.iRun, "images.remove": s.iRemove,
		"images.force": s.iForce, "images.table": s.iTable, "pull.ref": s.pullRef, "pull.go": s.pullGo,
		"run.go": s.runGo, "run.rm": s.runRm, "panel.close": s.panelClose,
		"volumes.table": s.vTable, "volumes.name": s.vName, "volumes.create": s.vCreate, "volumes.remove": s.vRemove,
		"volumes.force": s.vForce, "volumes.prune": s.vPrune, "volumes.all": s.vAll, "volumes.inspect": s.vInspect,
		"terminal": s.term,
	}
	for i, e := range s.runFields {
		m["run."+[]string{"image", "name", "command", "env", "entrypoint", "workdir", "memory", "volumes"}[i]] = e
	}
	return m
}

// Probe reports the scene's state.
func (s *Scene) Probe() Probe {
	a := s.app
	p := Probe{
		View: vm.ViewNames[a.View.Get()], Boxes: map[string]Box{},
		Selected: a.Containers.Selected.Get(), LastCommand: a.LastCommand.Get(), Hint: s.hint.Text().Get(),
		Error: a.Error.Get(), Live: a.Live.Get(), Pending: a.Pending.Get(), Running: a.Terminal.Running.Get(),
		Terminal: screenText(s.term.Screen()), Logs: screenText(s.logs.Screen()), Volumes: a.Volumes.Message.Get(),
		Dark:   s.dark,
		Status: []string{a.EngineVersion.Get(), a.Summary.Get(), a.Storage.Get(), a.Live.Get()},
	}
	shown := map[toolkit.Widget]bool{}
	for _, w := range s.widgets() {
		shown[w] = true
	}
	for name, w := range s.named() {
		if shown[w] {
			b := w.Bounds()
			p.Boxes[name] = Box{b.X, b.Y, b.W, b.H}
		}
		if w == s.focus {
			p.Focus = name
		}
	}
	for i := 0; i < a.Containers.Rows.Len(); i++ {
		p.Containers = append(p.Containers, a.Containers.Rows.At(i).Name)
	}
	for i := 0; i < a.Images.Rows.Len(); i++ {
		p.Images = append(p.Images, a.Images.Rows.At(i).Ref)
	}
	for i := 0; i < a.Volumes.Rows.Len(); i++ {
		p.VolumeNames = append(p.VolumeNames, a.Volumes.Rows.At(i).Name)
	}
	p.Mounts = a.Detail.Mounts.Slice()
	p.Memory, p.CPU = a.Detail.Memory.Get(), a.Detail.CPU.Get()
	return p
}
