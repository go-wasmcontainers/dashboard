package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-widgets/toolkit"
	"github.com/go-widgets/webcanvas"

	"github.com/go-wasmcontainers/dashboard/vm"
)

// ctrlClickRow adds body row i of a table to its selection.
func (r *rig) ctrlClickRow(t *toolkit.Table, i int) {
	r.t.Helper()
	r.s.Modifiers(webcanvas.Modifiers{Ctrl: true})
	r.clickRow(t, i)
	r.s.Modifiers(webcanvas.Modifiers{})
}

// TestMultiSelectRemove selects three of four rows with the mouse and
// presses Remove: the hover line names the three commands, the three go,
// the fourth stays.
func TestMultiSelectRemove(t *testing.T) {
	r := newRig(t)
	r.until("engine", func(p Probe) bool { return strings.HasPrefix(p.Status[0], "engine test") })
	for _, n := range []string{"v1", "v2", "v3", "v4"} {
		r.app.Volumes.NewName.Set(n)
		r.app.Volumes.Create()
		r.until("volume "+n, func(p Probe) bool { return slices.Contains(p.VolumeNames, n) })
	}
	for _, n := range []string{"n1", "n2", "n3", "n4"} {
		r.app.Networks.NewName.Set(n)
		r.app.Networks.Create()
		r.until("network "+n, func(p Probe) bool { return slices.Contains(p.Networks, n) })
	}

	for _, c := range []struct {
		view    int
		table   func() *toolkit.Table
		button  string
		names   func(Probe) []string
		keep    string
		targets func() int
		hover   func() string
	}{
		{vm.ViewVolumes, func() *toolkit.Table { return r.s.vTable }, "volumes.remove", func(p Probe) []string { return p.VolumeNames }, "v4",
			func() int { return len(r.app.Volumes.Targets()) }, func() string { return r.s.buttonLine(r.s.vRemove) }},
		{vm.ViewNetworks, func() *toolkit.Table { return r.s.nTable }, "networks.remove", func(p Probe) []string { return p.Networks }, "n4",
			func() int { return len(r.app.Networks.Targets()) }, func() string { return r.s.buttonLine(r.s.nRemove) }},
	} {
		r.app.View.Set(c.view)
		p := r.until("rows", func(p Probe) bool {
			return p.Boxes[map[int]string{vm.ViewVolumes: "volumes", vm.ViewNetworks: "networks"}[c.view]+".row.3"].H > 0
		})
		first := slices.Index(c.names(p), strings.TrimSuffix(c.keep, "4")+"1")
		r.clickRow(c.table(), first)
		r.ctrlClickRow(c.table(), first+1)
		r.ctrlClickRow(c.table(), first+2)
		if n := c.targets(); n != 3 {
			t.Fatalf("view %d: %d targets after a click and two Ctrl-clicks, want 3", c.view, n)
		}
		if h := c.hover(); !strings.HasPrefix(h, "Remove = 3 commands: ") || strings.Count(h, " rm ") != 3 {
			t.Errorf("hover = %q", h)
		}
		r.click(c.button)
		p = r.until("removed", func(p Probe) bool {
			return !slices.Contains(c.names(p), strings.TrimSuffix(c.keep, "4")+"1") && p.Pending == 0
		})
		for _, gone := range []string{"1", "2", "3"} {
			if slices.Contains(c.names(p), strings.TrimSuffix(c.keep, "4")+gone) {
				t.Errorf("view %d: %s%s is still there: %v", c.view, strings.TrimSuffix(c.keep, "4"), gone, c.names(p))
			}
		}
		if !slices.Contains(c.names(p), c.keep) {
			t.Errorf("view %d: the unselected %s was removed: %v", c.view, c.keep, c.names(p))
		}
		if p.Error != "" {
			t.Errorf("view %d: error %q", c.view, p.Error)
		}
	}
}
