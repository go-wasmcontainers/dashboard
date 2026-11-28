package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-widgets/toolkit"
	"github.com/go-widgets/webcanvas"
)

// TestTopologyView: a container on a network is a node on a hub, the speaker
// of a node with a sound stream mutes that container only, the group buttons
// mute the network, and the graph's accessibility tree says what a node is.
func TestTopologyView(t *testing.T) {
	r := newRig(t)
	r.until("engine", func(p Probe) bool { return strings.HasPrefix(p.Status[0], "engine test") })
	r.s.Modifiers(webcanvas.Modifiers{Alt: true})
	r.s.KeyDown("6")
	r.s.Modifiers(webcanvas.Modifiers{})
	r.click("terminal")
	r.typeText("wctr network create lab")
	r.s.KeyDown("Enter")
	r.until("lab", func(p Probe) bool { return slices.Contains(p.Networks, "lab") })
	r.typeText("wctr run -d --name web --network lab clock -interval 20ms")
	r.s.KeyDown("Enter")
	r.until("web listed", func(p Probe) bool { return len(p.Containers) == 1 && !p.Running })

	r.s.KeyDown("F6") // out of the terminal
	r.s.Modifiers(webcanvas.Modifiers{Alt: true})
	r.s.KeyDown("5")
	r.s.Modifiers(webcanvas.Modifiers{})
	p := r.until("topology", func(p Probe) bool {
		return p.View == "Topology" && slices.ContainsFunc(p.TopologyNodes, func(n string) bool { return strings.Contains(n, "web") })
	})
	if !slices.ContainsFunc(p.TopologyNodes, func(n string) bool { return strings.Contains(n, "lab") }) {
		t.Fatalf("no hub for lab: %v", p.TopologyNodes)
	}
	for _, name := range []string{"topology.graph", "topology.zoomin", "topology.muteall", "topology.import"} {
		if p.Boxes[name].W == 0 {
			t.Errorf("%s not on screen", name)
		}
	}
	if r.s.tImport.Disabled().Get() != true || r.s.tExport.Disabled().Get() != true {
		t.Error("compose transfer enabled on a page that has none")
	}

	// Zoom steps by 25 % and Fit changes it back into range.
	r.click("topology.zoomin")
	if z := r.s.Probe().TopologyZoom; z != 125 {
		t.Errorf("zoom = %d, want 125", z)
	}

	// The container has a sound stream: its node gets a speaker.
	id := r.app.Containers.Rows.At(0).ID
	r.app.Sound.SetActive([]string{id})
	node := "c:" + id
	var web toolkit.GraphNode
	for _, n := range r.s.tModel.Nodes.Slice() {
		if n.ID == node {
			web = n
		}
	}
	if len(web.Toggles) != 1 || !web.Toggles[0].On {
		t.Fatalf("speaker: %+v", web.Toggles)
	}
	r.s.tGraph.OnToggle(node, topoSoundToggle)
	if _, muted := r.app.Sound.Level(id); !muted {
		t.Error("the speaker did not mute the container")
	}
	for _, n := range r.s.tModel.Nodes.Slice() {
		if n.ID == node && n.Toggles[0].On {
			t.Error("the speaker still shows sound on")
		}
	}

	// A network's mute reaches its members; the hub's own toggle follows.
	r.app.Sound.SetMuted([]string{id}, false)
	r.app.Topology.Selected.Set("node:n:lab")
	if r.s.tMute.Disabled().Get() {
		t.Fatal("Mute disabled with a network selected")
	}
	r.click("topology.mute")
	if _, muted := r.app.Sound.Level(id); !muted {
		t.Error("Mute on a network did not mute its container")
	}
	if got := r.s.Probe().TopologyDetail; len(got) == 0 || !strings.HasPrefix(got[0], "Network lab") {
		t.Errorf("detail = %v", got)
	}
	r.click("topology.muteall")
	if !r.app.Sound.All.Get() || r.s.tMuteAll.Label().Get() != "Unmute all" {
		t.Error("Mute all did not set the page-wide mute")
	}

	// The accessibility tree names the node, its network and its state.
	r.s.tGraph.SetBounds(r.s.tGraph.Bounds())
	found := false
	for _, c := range r.s.tGraph.Children() {
		n := c.(toolkit.Accessible).A11y().Name
		if !strings.HasPrefix(n, "web, on lab") {
			continue
		}
		found = true
		if !strings.Contains(n, "muted") {
			t.Errorf("a11y name lacks the mute: %q", n)
		}
		kids := c.(interface{ Children() []toolkit.Widget }).Children()
		if len(kids) != 1 || kids[0].(toolkit.Accessible).A11y().Role != toolkit.RoleSwitch {
			t.Errorf("a11y toggles: %v", kids)
		}
	}
	if !found {
		t.Error("no tree item for web")
	}
}

// TestTopologyCompose: the import and export controls call the page, and
// export needs a compose project selected.
func TestTopologyCompose(t *testing.T) {
	r := newRig(t)
	var imported int
	var exported []string
	r.app.Topology.SetCompose(func() { imported++ }, func(p string) { exported = append(exported, p) })
	r.s.updateTopologyButtons()
	if r.s.tImport.Disabled().Get() || !r.s.tExport.Disabled().Get() {
		t.Fatal("import should be enabled, export not before a selection")
	}
	r.app.Topology.Import()
	if imported != 1 {
		t.Error("Import did not call the page")
	}
	r.app.Topology.Export()
	if len(exported) != 0 {
		t.Errorf("exported %v without a project", exported)
	}
}

// Refusals and previews reach the view: an import or export error and a sound
// refusal are on the summary line, a command's preview is in the detail pane.
func TestTopologyShowsRefusalsAndPreview(t *testing.T) {
	r := newRig(t)
	r.app.Topology.SetCompose(func() {}, func(string) {})
	r.app.Topology.ComposeFailed("import: refused")
	if got := r.s.tMsg.Text().Get(); !strings.Contains(got, "import: refused") {
		t.Errorf("summary = %q", got)
	}
	r.app.Topology.ComposeImported("shop") // a good import clears the error
	if got := r.s.tMsg.Text().Get(); strings.Contains(got, "refused") {
		t.Errorf("summary = %q", got)
	}
	r.app.Sound.SetAudio(func(string, float64, bool) bool { return false })
	r.app.Sound.ToggleMute("c1")
	if got := r.s.tMsg.Text().Get(); !strings.Contains(got, "page refused") {
		t.Errorf("a refused mute is not shown: %q", got)
	}
	r.app.Topology.ComposeDone([]string{"compose", "-p", "shop", "config"}, "name: shop")
	if !slices.Contains(r.s.tDetail.Items, "name: shop") {
		t.Errorf("preview not in the detail pane: %v", r.s.tDetail.Items)
	}
}

// A stopped container is drawn grey; a running one and a network keep the
// palette.
func TestTopologyNodeColor(t *testing.T) {
	r := newRig(t)
	r.s.tState = map[string]string{"c:run": "running", "c:stop": "exited"}
	for id, want := range map[string]bool{"c:run": false, "c:stop": true, "n:lab": false} {
		if _, got := r.s.tGraph.NodeColor(toolkit.GraphNode{ID: id}); got != want {
			t.Errorf("%s: grey = %v, want %v", id, got, want)
		}
	}
}

// The helpers that decide whether the widget's model must be rebuilt, on both
// sides of every comparison.
func TestTopologySameness(t *testing.T) {
	c1 := toolkit.GraphCluster{ID: "a", Label: "a"}
	c2 := toolkit.GraphCluster{ID: "b", Label: "b"}
	if !sameClusters([]toolkit.GraphCluster{c1, c2}, []toolkit.GraphCluster{c1, c2}) ||
		sameClusters([]toolkit.GraphCluster{c1, c2}, []toolkit.GraphCluster{c1}) ||
		sameClusters([]toolkit.GraphCluster{c1, c2}, []toolkit.GraphCluster{c1, c1}) {
		t.Error("sameClusters")
	}

	n := toolkit.GraphNode{ID: "x", Cluster: "k", Shape: toolkit.GraphShapeBox}
	for _, d := range []toolkit.GraphNode{{ID: "y", Cluster: "k"}, {ID: "x", Cluster: "z"}, {ID: "x", Cluster: "k", Shape: toolkit.GraphShapeHub}} {
		if sameIDs([]toolkit.GraphNode{n}, []toolkit.GraphNode{d}) {
			t.Errorf("sameIDs equal for %+v", d)
		}
	}
	if !sameIDs([]toolkit.GraphNode{n, n}, []toolkit.GraphNode{n, n}) || sameIDs([]toolkit.GraphNode{n}, []toolkit.GraphNode{n, n}) {
		t.Error("sameIDs")
	}

	b1, b2 := toolkit.GraphBadge{Text: "1"}, toolkit.GraphBadge{Text: "2"}
	t1, t2 := toolkit.GraphToggle{ID: "s", On: true}, toolkit.GraphToggle{ID: "s"}
	base := toolkit.GraphNode{Label: "l", Badges: []toolkit.GraphBadge{b1}, Toggles: []toolkit.GraphToggle{t1}}
	if !sameNode(base, base) {
		t.Error("sameNode of itself")
	}
	for name, d := range map[string]toolkit.GraphNode{
		"label":    {Label: "m", Badges: base.Badges, Toggles: base.Toggles},
		"category": {Label: "l", Category: "x", Badges: base.Badges, Toggles: base.Toggles},
		"badge":    {Label: "l", Badges: []toolkit.GraphBadge{b2}, Toggles: base.Toggles},
		"badges":   {Label: "l", Toggles: base.Toggles},
		"toggle":   {Label: "l", Badges: base.Badges, Toggles: []toolkit.GraphToggle{t2}},
		"toggles":  {Label: "l", Badges: base.Badges},
	} {
		if sameNode(base, d) {
			t.Errorf("sameNode equal when the %s differs", name)
		}
	}
}

// A hub shows a speaker only when one of its containers has a sound stream.
func TestTopologyHubSpeaker(t *testing.T) {
	r := newRig(t)
	r.app.Sound.SetActive([]string{"nobody"})
	if anyActive(r.app.Sound, []string{"x", "y"}) {
		t.Error("nothing is active")
	}
	r.app.Sound.SetActive([]string{"y"})
	if !anyActive(r.app.Sound, []string{"x", "y"}) {
		t.Error("y is active")
	}
	if anyActive(r.app.Sound, nil) {
		t.Error("no members")
	}
}
