package ui

import (
	"slices"
	"testing"

	"github.com/go-widgets/toolkit/iconoir"
	"github.com/go-widgets/webcanvas"

	"github.com/go-wasmcontainers/dashboard/vm"
)

// #34 item 7: a view change moves the keys to a widget of the new view. In the
// Terminal view they do not go to the hidden networks table, and Alt+<n> works
// at once, with no F6 first.
func TestViewChangeMovesFocus(t *testing.T) {
	r := newRig(t)
	alt := func(v int) {
		r.s.Modifiers(webcanvas.Modifiers{Alt: true})
		r.s.KeyDown(string(rune('1' + v)))
		r.s.Modifiers(webcanvas.Modifiers{})
	}
	alt(vm.ViewNetworks)
	r.until("networks", func(p Probe) bool { return p.View == "Networks" && len(p.Networks) > 0 })
	r.s.KeyDown("F6")
	r.s.KeyDown("F6")
	if r.s.focus != r.s.nTable {
		t.Fatalf("focus not on the table: %q", r.s.Probe().Focus)
	}
	alt(vm.ViewTerminal)
	if p := r.s.Probe(); p.View != "Terminal" || r.s.focus == r.s.nTable || !slices.Contains(r.s.widgets(), r.s.focus) {
		t.Fatalf("view %s: the keys stayed with a widget that is not shown (focus %q)", p.View, p.Focus)
	}
	// No F6 first: the next Alt+<n> is not swallowed by a terminal.
	alt(vm.ViewVolumes)
	if p := r.s.Probe(); p.View != "Volumes" {
		t.Errorf("Alt+n after a view change did not switch: %s", p.View)
	}
	// A focus that is still shown stays where it is.
	r.s.KeyDown("F6")
	r.s.KeyDown("F6")
	want := r.s.focus
	r.app.View.Set(vm.ViewVolumes)
	if r.s.focus != want {
		t.Error("the focus moved although its widget is still shown")
	}
}

// #34 item 8: every icon name the topology uses is in the Iconoir set.
func TestTopologyIconNamesExist(t *testing.T) {
	names := iconoir.Names()
	for _, n := range []string{topoIconHub, topoIconInternet, topoIconTab, topoIconContainer, topoIconSoundOn, topoIconSoundOff} {
		if !slices.Contains(names, n) {
			t.Errorf("iconoir has no %q", n)
		}
	}
}

// #34 item 8: nodes and speaker toggles carry an icon name, not a letter.
func TestTopologyNodesCarryIcons(t *testing.T) {
	r := newRig(t)
	if r.s.tGraph.Icons == nil {
		t.Fatal("GraphView.Icons is not set")
	}
	for _, c := range []struct {
		name string
		kind vm.TopoKind
		want string
	}{{"hub", vm.TopoNetwork, topoIconHub}, {"container", vm.TopoContainer, topoIconContainer}} {
		if got := topoNodeIcon(vm.TopoNode{Kind: c.kind}); got != c.want {
			t.Errorf("%s icon = %q, want %q", c.name, got, c.want)
		}
	}
	if in, tab := topoNodeIcon(vm.TopoNode{Kind: vm.TopoGateway, ID: vm.InternetNodeID}), topoNodeIcon(vm.TopoNode{Kind: vm.TopoGateway, ID: vm.HostNodeID}); in != topoIconInternet || tab != topoIconTab {
		t.Errorf("gateway icons %q %q", in, tab)
	}
	if topoNodeIcon(vm.TopoNode{Kind: vm.TopoGateway, ID: vm.InternetNodeID}) == topoNodeIcon(vm.TopoNode{Kind: vm.TopoGateway, ID: vm.HostNodeID}) {
		t.Error("the Internet and this tab share an icon")
	}
	if on, off := topoSpeaker(true), topoSpeaker(false); on.Icon != topoIconSoundOn || off.Icon != topoIconSoundOff || on.Glyph != "" || off.Glyph != "" || !on.On || off.On {
		t.Errorf("speaker toggles: %+v %+v", on, off)
	}
}

// #34 item 8: the icons are drawn. The same frame with the icon resolver taken
// away differs inside the graph, and only there.
func TestTopologyIconsArePainted(t *testing.T) {
	r := newRig(t)
	r.s.Modifiers(webcanvas.Modifiers{Alt: true})
	r.s.KeyDown(string(rune('1' + vm.ViewTopology)))
	r.s.Modifiers(webcanvas.Modifiers{})
	p := r.until("topology", func(p Probe) bool {
		return p.View == "Topology" && len(p.TopologyNodes) > 0
	})
	b := p.Boxes["topology.graph"]
	if b.W == 0 {
		t.Fatal("no graph on screen")
	}
	shot := func() []byte {
		r.s.invalidate()
		r.frame()
		return slices.Clone(r.buf)
	}
	with := shot()
	icons := r.s.tGraph.Icons
	r.s.tGraph.Icons = nil
	without := shot()
	r.s.tGraph.Icons = icons
	diff, outside := 0, 0
	w := 1280
	for i := 0; i < len(with); i += 4 {
		if slices.Equal(with[i:i+4], without[i:i+4]) {
			continue
		}
		x, y := (i/4)%w, (i/4)/w
		if x >= b.X && x < b.X+b.W && y >= b.Y && y < b.Y+b.H {
			diff++
		} else {
			outside++
		}
	}
	if diff < 30 {
		t.Errorf("only %d pixels of the graph change without the icons: they are not drawn", diff)
	}
	if outside != 0 {
		t.Errorf("%d pixels outside the graph changed", outside)
	}
}
