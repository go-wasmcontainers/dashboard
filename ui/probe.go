package ui

import (
	"fmt"

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
	Networks    []string
	// ContainerNetworks is the shown container's Networks tab.
	ContainerNetworks []string
	// Class is the class status line, ClassOpen the dialog, ClassError its
	// message, CodeLength how many characters the code field holds (never
	// the code itself).
	Class      string
	ClassOpen  bool
	ClassError string
	CodeLength int
	// Surfaces are the GPU surface panes, "title | status", and SurfaceBoxes
	// where each one's frame is drawn (canvas pixels; zero before a frame).
	Surfaces     []string
	SurfaceBoxes []Box
	// Shell is the screen of the shell inside a container, ShellLine its
	// command line, ShellStatus how it goes ("exited with status 3").
	Shell       string
	ShellLine   string
	ShellStatus string
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
		"containers.table": s.cTable, "detail": s.detail, "detail.follow": s.dFollow, "containers.shell": s.cShell,
		"images.search": s.iSearch, "images.pull": s.iPull, "images.run": s.iRun, "images.remove": s.iRemove,
		"images.force": s.iForce, "images.table": s.iTable, "pull.ref": s.pullRef, "pull.go": s.pullGo,
		"run.go": s.runGo, "run.rm": s.runRm, "panel.close": s.panelClose,
		"volumes.table": s.vTable, "volumes.name": s.vName, "volumes.create": s.vCreate, "volumes.remove": s.vRemove,
		"volumes.force": s.vForce, "volumes.prune": s.vPrune, "volumes.all": s.vAll, "volumes.inspect": s.vInspect,
		"terminal":       s.term,
		"networks.table": s.nTable, "networks.name": s.nName, "networks.create": s.nCreate, "networks.remove": s.nRemove,
		"networks.inspect": s.nInspect, "class.button": s.classBtn, "class.relay": s.jRelay, "class.name": s.jName,
		"class.code": s.jCode, "class.ok": s.jOK, "class.change": s.jChange, "class.cancel": s.jCancel,
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
	for i := 0; i < a.Networks.Rows.Len(); i++ {
		p.Networks = append(p.Networks, a.Networks.Rows.At(i).Name)
	}
	p.ContainerNetworks = a.Detail.Networks.Slice()
	p.Class, p.ClassOpen, p.ClassError = s.classInfo.Text().Get(), s.dialogOpen(), a.Class.Error.Get()
	p.CodeLength = len([]rune(s.jCode.Text().Get()))
	for _, pn := range s.panes {
		p.Surfaces = append(p.Surfaces, pn.title.Text().Get()+" | "+pn.status.Text().Get())
		r, _ := a.Surfaces.Find(pn.id)
		d := fit(pn.body, r.W, r.H)
		if !pn.shown {
			d = rect{}
		}
		p.SurfaceBoxes = append(p.SurfaceBoxes, Box{d.X, d.Y, d.W, d.H})
		if pn.closed && a.View.Get() == vm.ViewSurfaces {
			b := pn.dismiss.Bounds()
			p.Boxes[fmt.Sprintf("surface.%d.dismiss", pn.id)] = Box{b.X, b.Y, b.W, b.H}
		}
	}
	p.Shell = screenText(s.shellTerm.Screen())
	p.ShellLine, p.ShellStatus = a.ExecShell.Line.Get(), a.ExecShell.Status.Get()
	p.Mounts = a.Detail.Mounts.Slice()
	p.Memory, p.CPU = a.Detail.Memory.Get(), a.Detail.CPU.Get()
	return p
}
