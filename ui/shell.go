package ui

import (
	"github.com/go-widgets/toolkit"

	"github.com/go-wasmcontainers/dashboard/vm"
)

// shellTab is the index of the detail's Shell tab (after Logs, Inspect,
// Environment, Mounts, Networks, Stats).
const shellTab = 6

// buildShell adds the detail's Shell tab: a terminal whose keys go to the
// shell inside the container and whose screen shows its output. Like the
// other terminals, its screen has no reply channel and discards every
// OSC/DCS/APC string the container sends.
func (s *Scene) buildShell() {
	inv := s.invalidate
	a := s.app
	sh := a.ExecShell
	s.shellTerm = toolkit.NewTerminal(80, 24, 5000)
	s.shellTerm.Font = monoFont
	s.shellTerm.OnInput = sh.Input
	s.shellTerm.OnResize = func(cols, rows int) { sh.Resize(cols, rows) }
	sh.Out = func(b []byte) { s.shellTerm.Write(b); inv() }
	sh.Reset = func() {
		s.shellTerm.Screen().WriteString("\x1bc")
		s.shellTerm.Screen().ClearScrollback()
		inv()
	}
	s.detail.AddTab("Shell", s.shellTerm)
	d := a.Detail
	bindLabel(s.dState, func() string {
		if s.detail.Active().Get() == shellTab && sh.Line.Get() != "" {
			return "= " + sh.Line.Get() + " · " + sh.Status.Get()
		}
		return d.State.Get()
	}, inv, d.State, sh.Line, sh.Status, s.detail.Active())
	sh.Running.SubscribeChanged(func() { s.updateButtons(); inv() })
}

// openShell opens a shell in container r and shows it.
func (s *Scene) openShell(r vm.ContainerRow) {
	s.app.ExecShell.Open(r.ID, r.Ref())
	s.detail.Active().Set(shellTab)
	s.setFocus(s.detail)
	s.layout()
	s.invalidate()
}

// keyTarget is the widget keys and characters go to: the shell's terminal
// while the Shell tab is shown and the detail has the focus (the notebook
// would take the arrows for itself), else the focused widget.
func (s *Scene) keyTarget() toolkit.Widget {
	if s.focus == toolkit.Widget(s.detail) && s.detail.Active().Get() == shellTab {
		return s.shellTerm
	}
	return s.focus
}
