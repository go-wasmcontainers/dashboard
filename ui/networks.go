package ui

import (
	"github.com/go-widgets/toolkit"

	"github.com/go-wasmcontainers/dashboard/vm"
)

// codeMask is the rune the join code is drawn as. A masked Entry (toolkit
// v0.345.2) also keeps the code out of its accessibility value and off the
// clipboard.
const codeMask = '•'

// buildNetworks builds the networks view: the table of networks, create,
// remove, inspect, and the class status with its "Join class" dialog.
func (s *Scene) buildNetworks() {
	inv := s.invalidate
	a := s.app
	n := a.Networks
	cols := make([]toolkit.TableColumn, len(vm.NetworkColumns))
	for i, t := range vm.NetworkColumns {
		cols[i] = toolkit.TableColumn{Title: t}
	}
	s.nTable = toolkit.NewTable(cols, nil)
	s.nTable.MultiSelect = true
	s.nTable.Framed, s.nTable.RowLines = true, true
	bindRows(s.nTable, n.Rows, vm.NetworkRow.Cells, func() { s.syncNetworkSelection(); inv() })
	s.nTable.Selected().Subscribe(func(i int) {
		if i >= 0 && i < n.Rows.Len() {
			n.Selected.Set(n.Rows.At(i).Name)
		}
	})
	n.Selected.Subscribe(func(name string) {
		if name != "" {
			a.LastCommand.Set(n.InspectLine(name))
		}
		s.syncNetworkSelection()
		s.updateButtons()
		inv()
	})
	s.nMsg = toolkit.NewLabel("")
	bindLabel(s.nMsg, n.Message.Get, inv, n.Message)
	s.nName = toolkit.NewEntry("")
	s.nName.Placeholder = "new network name"
	bindObs(s.nName.Text(), n.NewName, inv)
	s.nCreate = button("Create", n.Create)
	s.nRemove = button("Remove", func() {
		if name := n.Selected.Get(); name != "" {
			n.Remove(name)
		}
	})
	s.nInspect = toolkit.NewListBox(nil)
	bindItems(s.nInspect, n.Inspect, inspectLine, inv)

	c := a.Class
	s.classInfo = toolkit.NewLabel("")
	bindLabel(s.classInfo, func() string {
		if !c.Available.Get() {
			return ""
		}
		if sum := c.Summary(); sum != "" {
			return sum
		}
		return "Not in a class."
	}, inv, c.Status, c.Available)
	s.classBtn = button("Join class…", func() {
		if c.Status.Get().State == "joined" {
			c.Leave()
			return
		}
		c.Show()
	})
	c.Status.Subscribe(func(st vm.ClassStatus) {
		if st.State == "joined" {
			s.classBtn.Label().Set("Leave class")
		} else {
			s.classBtn.Label().Set("Join class…")
		}
		inv()
	})
	s.jTitle = toolkit.NewLabel("Join a class")
	s.jRelayLbl, s.jNameLbl, s.jCodeLbl = toolkit.NewLabel("Relay"), toolkit.NewLabel("Your name"), toolkit.NewLabel("Join code")
	s.jRelay = toolkit.NewEntry("")
	s.jRelay.Placeholder = "https://relay.example:8443"
	bindObs(s.jRelay.Text(), c.RelayInput, inv)
	s.jRelayShown = toolkit.NewLabel("")
	bindLabel(s.jRelayShown, c.Relay.Get, inv, c.Relay)
	s.jName = toolkit.NewEntry("")
	s.jName.Placeholder = "optional: how the class sees you"
	bindObs(s.jName.Text(), c.Name, inv)
	s.jCode = toolkit.NewEntry("")
	s.jCode.Mask = codeMask
	s.jCode.Placeholder = "the code your teacher gave"
	bindObs(s.jCode.Text(), c.Code, inv)
	s.jOK = button("Continue", func() {
		if c.Relay.Get() == "" {
			c.Continue()
		} else {
			c.Join()
		}
	})
	s.jCode.OnSubmit = func(string) { c.Join() }
	c.Busy.Subscribe(func(b bool) { s.classBtn.Disabled().Set(b); s.jOK.Disabled().Set(b); inv() })
	s.jChange = button("Change relay", c.ChangeRelay)
	s.jCancel = button("Cancel", c.Cancel)
	s.jErr = toolkit.NewLabel("")
	bindLabel(s.jErr, c.Error.Get, inv, c.Error)
	s.jNote = toolkit.NewLabel("")
	bindLabel(s.jNote, func() string {
		if c.Relay.Get() == "" {
			return "Only https:// (or wss://) relays. The page restarts the engine with it, then asks for the code."
		}
		return "The code goes to the engine once; it is never shown, stored, logged or put in a URL."
	}, inv, c.Relay)
	refreshOK := func() {
		if c.Relay.Get() == "" {
			s.jOK.Label().Set("Continue")
		} else {
			s.jOK.Label().Set("Join")
		}
	}
	refreshOK()
	c.Relay.SubscribeChanged(func() { refreshOK(); s.layout(); inv() })
	c.Open.Subscribe(func(open bool) {
		if open {
			a.View.Set(vm.ViewNetworks)
			if c.Relay.Get() == "" {
				s.setFocus(s.jRelay)
			} else {
				s.setFocus(s.jName)
			}
		} else if s.inDialog(s.focus) {
			// Focus never stays on a control that is no longer shown.
			s.setFocus(s.classBtn)
		}
		s.layout()
		inv()
	})
	c.Available.SubscribeChanged(func() { s.layout(); inv() })
}

func (s *Scene) syncNetworkSelection() {
	n := s.app.Networks
	sel := -1
	for i := 0; i < n.Rows.Len(); i++ {
		if n.Rows.At(i).Name == n.Selected.Get() {
			sel = i
		}
	}
	s.nTable.Selected().Set(sel)
}

// inDialog says whether w is one of the join dialog's controls.
func (s *Scene) inDialog(w toolkit.Widget) bool {
	for _, d := range []toolkit.Widget{s.jRelay, s.jName, s.jCode, s.jOK, s.jChange, s.jCancel} {
		if w == d {
			return true
		}
	}
	return false
}

// dialogOpen says whether the join dialog is shown.
func (s *Scene) dialogOpen() bool { return s.app.Class.Available.Get() && s.app.Class.Open.Get() }

// layoutNetworks places the networks view in bar and body.
func (s *Scene) layoutNetworks(bar, body rect) {
	g := u(gap)
	line, field := u(22), u(26)
	if s.app.Class.Available.Get() {
		row(bar, s.nName, 200, s.nCreate, 80, s.nRemove, 90, s.classBtn, 120, s.classInfo, 0)
	} else {
		row(bar, s.nName, 200, s.nCreate, 80, s.nRemove, 90)
	}
	s.nMsg.SetBounds(rect{X: body.X, Y: body.Y, W: body.W, H: line})
	y := body.Y + line + g
	if s.dialogOpen() {
		row(rect{X: body.X, Y: y, W: body.W, H: field}, s.jTitle, 0, s.jCancel, 80)
		y += field + g
		half := rect{X: body.X, W: min(body.W, u(560)), H: field}
		if s.app.Class.Relay.Get() == "" {
			half.Y = y
			row(half, s.jRelayLbl, 90, s.jRelay, 0, s.jOK, 90)
			y += field + g
		} else {
			half.Y = y
			row(half, s.jRelayLbl, 90, s.jRelayShown, 0, s.jChange, 120)
			y += field + g
			half.Y = y
			row(half, s.jNameLbl, 90, s.jName, 0)
			y += field + g
			half.Y = y
			row(half, s.jCodeLbl, 90, s.jCode, 0, s.jOK, 90)
			y += field + g
		}
		s.jErr.SetBounds(rect{X: body.X, Y: y, W: body.W, H: line})
		y += line
		s.jNote.SetBounds(rect{X: body.X, Y: y, W: body.W, H: line})
		y += line + g
	}
	rest := body.Y + body.H - y
	th := rest * 55 / 100
	s.nTable.SetBounds(rect{X: body.X, Y: y, W: body.W, H: th})
	iy := y + th + g
	s.nInspect.SetBounds(rect{X: body.X, Y: iy, W: body.W, H: body.Y + body.H - iy})
}

// networkWidgets are the networks view's interactive widgets, in focus order.
func (s *Scene) networkWidgets() []toolkit.Widget {
	out := []toolkit.Widget{s.nName, s.nCreate, s.nRemove}
	if s.app.Class.Available.Get() {
		out = append(out, s.classBtn)
	}
	if s.dialogOpen() {
		if s.app.Class.Relay.Get() == "" {
			out = append(out, s.jRelay, s.jOK)
		} else {
			out = append(out, s.jChange, s.jName, s.jCode, s.jOK)
		}
		out = append(out, s.jCancel)
	}
	return append(out, s.nTable, s.nInspect)
}

// networkLabels are the networks view's display-only widgets.
func (s *Scene) networkLabels() []toolkit.Widget {
	out := []toolkit.Widget{s.nMsg}
	if s.app.Class.Available.Get() {
		out = append(out, s.classInfo)
	}
	if s.dialogOpen() {
		out = append(out, s.jTitle, s.jRelayLbl, s.jErr, s.jNote)
		if s.app.Class.Relay.Get() != "" {
			out = append(out, s.jRelayShown, s.jNameLbl, s.jCodeLbl)
		}
	}
	return out
}
