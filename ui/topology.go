package ui

import (
	"fmt"

	"github.com/go-widgets/toolkit"

	"github.com/go-wasmcontainers/dashboard/vm"
)

// Graph geometry, in logical pixels.
const (
	topoNodeW  = 124
	topoRadius = 4
)

// topoSoundToggle is the id of the speaker toggle of a node.
const topoSoundToggle = "sound"

// okabeIto is the colour-blind-safe categorical palette (Okabe and Ito, 2008),
// the toolkit's graph default and agentscope's.
var okabeIto = []toolkit.RGBA{
	toolkit.RGB(0xE6, 0x9F, 0x00), toolkit.RGB(0x56, 0xB4, 0xE9), toolkit.RGB(0x00, 0x9E, 0x73), toolkit.RGB(0xF0, 0xE4, 0x42),
	toolkit.RGB(0x00, 0x72, 0xB2), toolkit.RGB(0xD5, 0x5E, 0x00), toolkit.RGB(0xCC, 0x79, 0xA7), toolkit.RGB(0x99, 0x99, 0x99),
}

// mix returns a moved toward b by t (0..1), per channel.
func mix(a, b toolkit.RGBA, t float64) toolkit.RGBA {
	m := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t + 0.5) }
	return toolkit.RGBA{R: m(a.R, b.R), G: m(a.G, b.G), B: m(a.B, b.B), A: 0xFF}
}

// topoPalette is the categories' colours in a theme, as agentscope has them:
// Okabe-Ito mixed 12 % toward white on the dark ground, 6 % toward slate-900
// (#0F172A) on the light one, so no hue glares or fades into its ground.
func topoPalette(dark bool) []toolkit.RGBA {
	to, t := toolkit.RGB(0x0F, 0x17, 0x2A), 0.06
	if dark {
		to, t = toolkit.RGB(0xFF, 0xFF, 0xFF), 0.12
	}
	out := make([]toolkit.RGBA, len(okabeIto))
	for i, c := range okabeIto {
		out[i] = mix(c, to, t)
	}
	return out
}

// stoppedGrey is the colour of a container that is not running.
var stoppedGrey = toolkit.RGB(0x7A, 0x82, 0x8C)

// buildTopology builds the Topology view: the graph (networks as hubs,
// containers in their compose project's cluster, "This tab" and "Internet" as
// gateways), the toolbar, and the detail pane of the selection.
func (s *Scene) buildTopology() {
	inv := s.invalidate
	a := s.app
	t := a.Topology

	s.tModel = toolkit.NewGraphViewModel()
	g := toolkit.NewGraphView(s.tModel)
	g.Layout = toolkit.GraphLayoutBands
	g.ShowLegend = true
	g.NodeWidth = topoNodeW
	g.CornerRadius = topoRadius
	g.Palette = topoPalette(s.dark)
	g.NodeColor = func(n toolkit.GraphNode) (toolkit.RGBA, bool) {
		if st, ok := s.tState[n.ID]; ok && st != "running" {
			return stoppedGrey, true
		}
		return toolkit.RGBA{}, false
	}
	g.OnToggle = func(nodeID, toggleID string) {
		if toggleID == topoSoundToggle {
			t.ToggleSound(nodeID)
		}
	}
	s.tGraph = g
	bindObs(s.tModel.Selected, t.Selected, inv)

	s.tZoomOut = button("Zoom out", func() { s.tGraph.ZoomOut(); inv() })
	s.tZoomIn = button("Zoom in", func() { s.tGraph.ZoomIn(); inv() })
	s.tFit = button("Fit", func() { s.tGraph.FitToView(); inv() })
	s.tMute = button("Mute", t.MuteSelected)
	s.tMuteProject = button("Mute project", t.MuteSelectedProject)
	s.tMuteAll = button("Mute all", func() { a.Sound.SetAllMuted(!a.Sound.All.Get()) })
	s.tImport = button("Import compose…", t.Import)
	s.tExport = button("Export compose", t.Export)

	s.tMsg = toolkit.NewLabel("")
	bindLabel(s.tMsg, func() string { return topologySummary(t.Graph()) }, inv, t.Revision)
	s.tDetail = toolkit.NewListBox(nil)
	// The layout is the toolkit's: the summary line over the graph and its
	// detail pane (a 68/32 split). The boxes only place their children; the
	// children stay the scene's own widgets.
	s.tSplit = toolkit.NewHBox()
	s.tSplit.AddFlex(s.tGraph, 68)
	s.tSplit.AddFlex(s.tDetail, 32)
	s.tColumn = toolkit.NewVBox()
	s.tColumn.AddFixed(s.tMsg, u(22))
	s.tColumn.AddFlex(s.tSplit, 1)

	refresh := func() { s.syncTopology(); s.updateTopologyButtons(); inv() }
	t.Revision.SubscribeChanged(refresh)
	a.Sound.Revision.SubscribeChanged(refresh)
	a.Sound.All.SubscribeChanged(refresh)
	t.Selected.SubscribeChanged(func() { s.syncTopologyDetail(); s.updateTopologyButtons(); inv() })
	s.syncTopology()
	s.updateTopologyButtons()
}

// topologySummary is the line above the graph: what it holds, and what was
// left out when a bound was reached.
func topologySummary(g vm.Graph) string {
	nets, cs := 0, 0
	for _, n := range g.Nodes {
		switch n.Kind {
		case vm.TopoNetwork:
			nets++
		case vm.TopoContainer:
			cs++
		}
	}
	line := fmt.Sprintf("%d networks, %d containers", nets, cs)
	if g.Note != "" {
		line += " (" + g.Note + ")"
	}
	return line
}

// syncTopology copies the viewmodel's graph and the sound state into the
// widget's model. The nodes are replaced one by one when the set did not change,
// so a mute does not move the picture; edges are free to change at any time.
func (s *Scene) syncTopology() {
	t, snd := s.app.Topology, s.app.Sound
	g := t.Graph()

	clusters := make([]toolkit.GraphCluster, 0, len(g.Clusters))
	for _, c := range g.Clusters {
		clusters = append(clusters, toolkit.GraphCluster{ID: c, Label: c})
	}
	s.tState = map[string]string{}
	nodes := make([]toolkit.GraphNode, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		gn := toolkit.GraphNode{ID: n.ID, Label: n.Label, Cluster: n.Cluster, Category: n.Category}
		for _, b := range n.Badges {
			gn.Badges = append(gn.Badges, toolkit.GraphBadge{Text: b})
		}
		switch n.Kind {
		case vm.TopoNetwork:
			gn.Shape = toolkit.GraphShapeHub
			gn.Cluster = ""
			members := t.NodeMembers(n)
			if anyActive(snd, members) {
				gn.Toggles = []toolkit.GraphToggle{{ID: topoSoundToggle, Glyph: "S", Label: "Sound of " + n.Label, On: !snd.AllMuted(members)}}
			}
		case vm.TopoGateway:
			gn.Shape = toolkit.GraphShapeGateway
			gn.Cluster = ""
		case vm.TopoContainer:
			s.tState[n.ID] = n.State
			if snd.Active(n.Container) {
				vol, muted := snd.Level(n.Container)
				txt := fmt.Sprintf("%d%%", vol)
				if muted || snd.All.Get() {
					txt = "muted"
				}
				gn.Badges = append(gn.Badges, toolkit.GraphBadge{Text: txt})
				gn.Toggles = []toolkit.GraphToggle{{ID: topoSoundToggle, Glyph: "S", Label: "Sound of " + n.Label, On: !muted}}
			}
		}
		nodes = append(nodes, gn)
	}

	m := s.tModel
	if !sameClusters(m.Clusters.Slice(), clusters) {
		m.Clusters.Clear()
		m.Clusters.Append(clusters...)
	}
	cur := m.Nodes.Slice()
	if sameIDs(cur, nodes) {
		for i := range nodes {
			if !sameNode(cur[i], nodes[i]) {
				m.Nodes.Set(i, nodes[i])
			}
		}
	} else {
		m.Nodes.Clear()
		m.Nodes.Append(nodes...)
	}
	edges := make([]toolkit.GraphEdge, 0, len(g.Edges))
	for _, e := range g.Edges {
		edges = append(edges, toolkit.GraphEdge{ID: e.ID, From: e.From, To: e.To, Label: e.Label, State: toolkit.GraphEdgeState(e.State)})
	}
	m.Edges.Clear()
	m.Edges.Append(edges...)
	s.syncTopologyDetail()
}

func anyActive(snd *vm.Sound, ids []string) bool {
	for _, id := range ids {
		if snd.Active(id) {
			return true
		}
	}
	return false
}

func sameClusters(a, b []toolkit.GraphCluster) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameIDs(a, b []toolkit.GraphNode) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Cluster != b[i].Cluster || a[i].Shape != b[i].Shape {
			return false
		}
	}
	return true
}

func sameNode(a, b toolkit.GraphNode) bool {
	if a.Label != b.Label || a.Category != b.Category || len(a.Badges) != len(b.Badges) || len(a.Toggles) != len(b.Toggles) {
		return false
	}
	for i := range a.Badges {
		if a.Badges[i] != b.Badges[i] {
			return false
		}
	}
	for i := range a.Toggles {
		if a.Toggles[i] != b.Toggles[i] {
			return false
		}
	}
	return true
}

// syncTopologyDetail fills the detail pane with the selection's text.
func (s *Scene) syncTopologyDetail() {
	lines := s.app.Topology.Detail()
	if len(lines) == 0 {
		lines = []string{"Select a node or an edge to see what it is."}
	}
	s.tDetail.Items = append(s.tDetail.Items[:0], lines...)
}

// updateTopologyButtons enables the controls the selection and the page allow.
func (s *Scene) updateTopologyButtons() {
	t := s.app.Topology
	n, ok := t.SelectedNode()
	s.tMute.Disabled().Set(!ok || len(t.NodeMembers(n)) == 0)
	s.tMuteProject.Disabled().Set(t.SelectedProject() == "")
	s.tImport.Disabled().Set(!t.CanImport())
	s.tExport.Disabled().Set(!t.CanExport())
	if s.app.Sound.All.Get() {
		s.tMuteAll.Label().Set("Unmute all")
	} else {
		s.tMuteAll.Label().Set("Mute all")
	}
}

// applyTopologyTheme gives the graph the palette of the theme.
func (s *Scene) applyTopologyTheme() {
	s.tGraph.Palette = topoPalette(s.dark)
}

// layoutTopology places the topology view in bar and body: the graph on the
// left, the detail pane on the right.
func (s *Scene) layoutTopology(bar, body rect) {
	g := u(gap)
	row(bar, s.tZoomOut, 84, s.tZoomIn, 80, s.tFit, 50, s.tMute, 70, s.tMuteProject, 110, s.tMuteAll, 90, s.tImport, 140, s.tExport, 130)
	s.tColumn.Spacing, s.tSplit.Spacing = g, g
	s.tColumn.SetBounds(body)
}

func (s *Scene) topologyWidgets() []toolkit.Widget {
	return []toolkit.Widget{s.tZoomOut, s.tZoomIn, s.tFit, s.tMute, s.tMuteProject, s.tMuteAll, s.tImport, s.tExport, s.tGraph, s.tDetail}
}

func (s *Scene) topologyLabels() []toolkit.Widget { return []toolkit.Widget{s.tMsg} }

// topologyProbe lists the graph for tests: "<kind> <label> | <cluster>".
func (s *Scene) topologyProbe() (nodes, edges []string) {
	g := s.app.Topology.Graph()
	for _, n := range g.Nodes {
		nodes = append(nodes, fmt.Sprintf("%d %s | %s", n.Kind, n.Label, n.Cluster))
	}
	for _, e := range g.Edges {
		edges = append(edges, e.Label)
	}
	return nodes, edges
}
