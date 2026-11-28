package vm

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/go-wasmcontainers/engine/api"
	"github.com/go-widgets/mvvm"

	"github.com/go-wasmcontainers/dashboard/engine"
)

// Bounds on what the topology draws: a page with more is truncated and says so.
const (
	MaxTopoNetworks   = 64
	MaxTopoContainers = 256
	maxTopoEdges      = 2048
	maxTopoPorts      = 16 // port badges per container
	maxEgressJSON     = 1 << 20
)

// Labels and names the projection reads.
const (
	composeProjectLabel = "com.docker.compose.project"
	standaloneCluster   = "standalone"
	hostNodeID          = "g:host"
	internetNodeID      = "g:internet"
)

// TopoKind is what a node of the topology stands for.
type TopoKind int

const (
	// TopoContainer is a container.
	TopoContainer TopoKind = iota
	// TopoNetwork is a network, drawn as a hub its containers attach to.
	TopoNetwork
	// TopoGateway is the edge of the picture: this browser tab (published
	// ports come in through it) or the Internet (egress goes out through it).
	TopoGateway
)

// TopoEdgeState says how an edge is drawn.
type TopoEdgeState int

const (
	// EdgeNormal is a live path.
	EdgeNormal TopoEdgeState = iota
	// EdgeDenied is a path the engine refuses (no egress).
	EdgeDenied
	// EdgeIdle is a path that exists but is down, or not known.
	EdgeIdle
)

// TopoNode is one node of the graph.
type TopoNode struct {
	ID, Label, Cluster, Category string
	Kind                         TopoKind
	// Container is the container's full ID, the audio key (TopoContainer).
	Container string
	// Network is the network's name (TopoNetwork).
	Network string
	// State is the container's state ("running", "exited"…).
	State string
	// Badges are short chips: the ports a container exposes but does not publish.
	Badges []string
	// Detail are the lines of the detail pane: plain text, cleaned and bounded
	// (names, labels and relay strings come from outside the dashboard).
	Detail []string
}

// TopoEdge is one directed edge: an attachment (container to network), a
// published port (this tab to container) or an egress path (network to Internet).
type TopoEdge struct {
	ID, From, To, Label string
	State               TopoEdgeState
}

// Graph is the topology as data: no toolkit type, so it is tested without a screen.
type Graph struct {
	// Clusters are the cluster names in drawing order (projects, then standalone).
	Clusters []string
	Nodes    []TopoNode
	Edges    []TopoEdge
	// Note says what was left out (a bound was reached), or "".
	Note string
}

// NetworkMembers are the full IDs of the containers attached to a network.
func (g Graph) NetworkMembers(network string) []string {
	var out []string
	hub := "n:" + network
	for _, e := range g.Edges {
		if e.To == hub && strings.HasPrefix(e.ID, "a:") {
			out = append(out, strings.TrimPrefix(e.From, "c:"))
		}
	}
	sort.Strings(out)
	return out
}

// ProjectMembers are the full IDs of the containers of a compose project (or
// of "standalone").
func (g Graph) ProjectMembers(project string) []string {
	var out []string
	for _, n := range g.Nodes {
		if n.Kind == TopoContainer && n.Cluster == project {
			out = append(out, n.Container)
		}
	}
	sort.Strings(out)
	return out
}

// Node returns the node with the id.
func (g Graph) Node(id string) (TopoNode, bool) {
	for _, n := range g.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return TopoNode{}, false
}

// Egress is what a network says of its way out, decoded from the network's
// inspect document. An engine that does not report it leaves Policy "": the
// graph then draws "no egress reported", it does not fail.
type Egress struct {
	// Policy is "none", "internal", "gateway" or "unknown" ("" when absent).
	Policy string
	Reason string
	// GatewayKind and GatewayState describe the gateway of a "gateway" policy.
	GatewayKind, GatewayState string
	// Allowed is how many host:port pairs the allowlist holds.
	Allowed int
	// Operator is the label the relay's operator set; DeniedHost and
	// DeniedReason are the last destination the relay refused. All three come
	// from the relay, a party outside the engine: they are plain text, cleaned
	// and bounded to maxRelayText runes, and nothing of them is interpreted.
	Operator, DeniedHost, DeniedReason string
}

// maxRelayText bounds each string the relay supplies (operator, denial).
const maxRelayText = 48

// ParseEgress decodes the Egress object of a network inspect document. It
// never fails: a document that is empty, oversized, not JSON, or without an
// Egress object yields the zero Egress ("no egress reported"). Every string is
// cleaned for display.
func ParseEgress(raw []byte) Egress {
	if len(raw) == 0 || len(raw) > maxEgressJSON {
		return Egress{}
	}
	var doc struct {
		Egress *struct {
			Policy  string `json:"Policy"`
			Reason  string `json:"Reason"`
			Gateway *struct {
				Kind     string `json:"Kind"`
				Operator string `json:"Operator"`
				State    string `json:"State"`
			} `json:"Gateway"`
			Allowlist []json.RawMessage `json:"Allowlist"`
			Counters  struct {
				LastDenied *struct {
					Host   string `json:"Host"`
					Reason string `json:"Reason"`
				} `json:"LastDenied"`
			} `json:"Counters"`
		} `json:"Egress"`
	}
	if json.Unmarshal(raw, &doc) != nil || doc.Egress == nil {
		return Egress{}
	}
	e := Egress{Policy: Clean(strings.ToLower(doc.Egress.Policy), 16), Reason: Clean(doc.Egress.Reason, 120), Allowed: len(doc.Egress.Allowlist)}
	if g := doc.Egress.Gateway; g != nil {
		e.GatewayKind, e.GatewayState = Clean(g.Kind, 32), Clean(strings.ToLower(g.State), 32)
		e.Operator = Clean(g.Operator, maxRelayText)
	}
	if d := doc.Egress.Counters.LastDenied; d != nil {
		e.DeniedHost, e.DeniedReason = Clean(d.Host, maxRelayText), Clean(d.Reason, maxRelayText)
	}
	return e
}

// PortBinding is one entry of a container summary's Ports.
type PortBinding struct {
	IP              string
	Private, Public int
	Proto           string
}

// ParsePorts decodes a summary's Ports (the engine sends api.Port values, a
// remote one decodes to maps): entries it cannot read are skipped, at most 256 kept.
func ParsePorts(list []any) []PortBinding {
	var out []PortBinding
	for _, x := range list {
		if len(out) >= 256 {
			break
		}
		b, _ := json.Marshal(x) // a decoded JSON value always marshals; a failure leaves b empty and the unmarshal below skips it
		var p api.Port
		if json.Unmarshal(b, &p) != nil || p.PrivatePort == 0 {
			continue
		}
		proto := Clean(p.Type, 8)
		if proto == "" {
			proto = "tcp"
		}
		out = append(out, PortBinding{IP: Clean(p.IP, 64), Private: int(p.PrivatePort), Public: int(p.PublicPort), Proto: proto})
	}
	return out
}

// TopoInput is what the graph is built from.
type TopoInput struct {
	Containers []api.ContainerSummary
	Networks   []engine.Network
	// Egress is each network's egress, by name (absent: not reported).
	Egress map[string]Egress
}

func containerName(c api.ContainerSummary) string {
	name := ""
	if len(c.Names) > 0 {
		name = strings.TrimPrefix(c.Names[0], "/")
	}
	if name == "" {
		name = ShortID(c.ID)
	}
	return Clean(name, 64)
}

// BuildGraph projects containers and networks onto a graph: networks are hubs,
// attachments are edges, projects are clusters; published ports come in from
// "This tab", and each network reaches the Internet through its egress.
func BuildGraph(in TopoInput) Graph {
	var g Graph
	var notes []string

	cs := in.Containers
	if len(cs) > MaxTopoContainers {
		notes = append(notes, fmt.Sprintf("%d of %d containers shown", MaxTopoContainers, len(cs)))
		cs = cs[:MaxTopoContainers]
	}
	nets := append([]engine.Network(nil), in.Networks...)
	sort.Slice(nets, func(i, j int) bool { return nets[i].Name < nets[j].Name })
	if len(nets) > MaxTopoNetworks {
		notes = append(notes, fmt.Sprintf("%d of %d networks shown", MaxTopoNetworks, len(nets)))
		nets = nets[:MaxTopoNetworks]
	}

	// Containers, in the order of their clusters.
	type cn struct {
		sum  api.ContainerSummary
		node TopoNode
		nets []string // "network (ip)", for the detail pane
		pubs []string // published ports, for the detail pane
	}
	byID := map[string]*cn{}
	var order []*cn
	projects := map[string]bool{}
	for _, c := range cs {
		if c.ID == "" {
			continue
		}
		cluster := Clean(c.Labels[composeProjectLabel], 64)
		if cluster == "" {
			cluster = standaloneCluster
		}
		if cluster != standaloneCluster {
			projects[cluster] = true
		}
		n := TopoNode{ID: "c:" + c.ID, Label: containerName(c), Cluster: cluster, Category: cluster, Kind: TopoContainer, Container: c.ID, State: Clean(c.State, 16)}
		x := &cn{sum: c, node: n}
		byID[c.ID] = x
		order = append(order, x)
	}
	for p := range projects {
		g.Clusters = append(g.Clusters, p)
	}
	sort.Strings(g.Clusters)
	g.Clusters = append(g.Clusters, standaloneCluster)
	rank := map[string]int{}
	for i, c := range g.Clusters {
		rank[c] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := order[i].node, order[j].node
		if rank[a.Cluster] != rank[b.Cluster] {
			return rank[a.Cluster] < rank[b.Cluster]
		}
		return a.Label < b.Label
	})

	find := func(id string) *cn {
		if x, ok := byID[id]; ok {
			return x
		}
		for full, x := range byID {
			if ShortID(full) == ShortID(id) {
				return x
			}
		}
		return nil
	}

	addEdge := func(e TopoEdge) {
		if len(g.Edges) < maxTopoEdges {
			g.Edges = append(g.Edges, e)
		}
	}

	needHost, needInternet := false, false
	// Networks: a hub each, an attachment edge per member, an egress edge.
	for _, nw := range nets {
		hub := TopoNode{ID: "n:" + nw.Name, Label: Clean(nw.Name, 64), Category: "network", Kind: TopoNetwork, Network: nw.Name}
		hubAt := len(g.Nodes)
		g.Nodes = append(g.Nodes, hub)
		ids := make([]string, 0, len(nw.Containers))
		for id := range nw.Containers {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		members := 0
		for _, id := range ids {
			x := find(id)
			if x == nil {
				continue
			}
			members++
			x.nets = append(x.nets, strings.TrimSpace(Clean(nw.Name, 64)+" "+Clean(nw.Containers[id].IPv4Address, 64)))
			addEdge(TopoEdge{ID: "a:" + nw.Name + ":" + x.sum.ID, From: x.node.ID, To: hub.ID, Label: Clean(nw.Containers[id].IPv4Address, 64)})
		}
		eg := in.Egress[nw.Name]
		if eg.DeniedHost != "" {
			d := "last denied " + eg.DeniedHost
			if eg.DeniedReason != "" {
				d += " (" + eg.DeniedReason + ")"
			}
			g.Nodes[hubAt].Badges = []string{d}
		}
		edge := egressEdge(nw, eg)
		g.Nodes[hubAt].Detail = networkDetail(nw, eg, members, edge.Label)
		needInternet = true
		addEdge(edge)
	}

	// Published ports and host networking come in through "This tab".
	for _, x := range order {
		var badges []string
		exposed := map[string]bool{}
		published := map[string]bool{}
		for _, p := range ParsePorts(x.sum.Ports) {
			key := fmt.Sprintf("%d/%s", p.Private, p.Proto)
			if p.Public == 0 {
				exposed[key] = true
				continue
			}
			published[key] = true
			ip := p.IP
			if ip == "" || ip == "0.0.0.0" || ip == "::" {
				ip = "127.0.0.1"
			}
			needHost = true
			label := fmt.Sprintf("%s:%d -> %s", ip, p.Public, key)
			x.pubs = append(x.pubs, label)
			addEdge(TopoEdge{ID: fmt.Sprintf("p:%s:%s:%d", x.sum.ID, key, p.Public), From: hostNodeID, To: x.node.ID, Label: label})
		}
		for k := range exposed {
			if !published[k] {
				badges = append(badges, k)
			}
		}
		sort.Strings(badges)
		if len(badges) > maxTopoPorts {
			badges = append(badges[:maxTopoPorts], fmt.Sprintf("+%d", len(badges)-maxTopoPorts))
		}
		x.node.Badges = badges
		host := x.sum.HostConfig.NetworkMode == "host"
		if host {
			needHost = true
			addEdge(TopoEdge{ID: "h:" + x.sum.ID, From: hostNodeID, To: x.node.ID, Label: "host network"})
		}
		x.node.Detail = containerDetail(x.node, ShortID(x.sum.ID), x.nets, x.pubs, badges, host)
	}
	if needHost {
		g.Nodes = append(g.Nodes, TopoNode{ID: hostNodeID, Label: "This tab", Category: "gateway", Kind: TopoGateway,
			Detail: []string{"This browser tab", "Published ports come in through it, on the address the engine bound."}})
	}
	if needInternet {
		g.Nodes = append(g.Nodes, TopoNode{ID: internetNodeID, Label: "Internet", Category: "gateway", Kind: TopoGateway,
			Detail: []string{"The Internet", "Each network's way out is the edge it draws to here."}})
	}
	for _, x := range order {
		g.Nodes = append(g.Nodes, x.node)
	}
	g.Note = strings.Join(notes, "; ")
	return g
}

func containerDetail(n TopoNode, short string, nets, pubs, exposed []string, host bool) []string {
	out := []string{"Container " + n.Label, "ID " + short, "State " + n.State, "Project " + n.Cluster}
	if host {
		out = append(out, "Network host (the class relay)")
	}
	for _, s := range nets {
		out = append(out, "On "+s)
	}
	for _, s := range pubs {
		out = append(out, "Publishes "+s)
	}
	for _, s := range exposed {
		out = append(out, "Exposes "+s)
	}
	return out
}

func networkDetail(nw engine.Network, e Egress, members int, egress string) []string {
	out := []string{"Network " + Clean(nw.Name, 64), fmt.Sprintf("%d containers attached", members)}
	if d := Clean(nw.Driver, 32); d != "" {
		out = append(out, "Driver "+d)
	}
	out = append(out, "Egress: "+egress)
	if e.Policy != "" {
		out = append(out, "Policy "+e.Policy)
	}
	if e.GatewayKind != "" {
		out = append(out, "Gateway "+e.GatewayKind+" "+e.GatewayState)
	}
	if e.Operator != "" {
		out = append(out, "Operator "+e.Operator)
	}
	if e.DeniedHost != "" {
		out = append(out, "Last denied "+e.DeniedHost+" "+e.DeniedReason)
	}
	return out
}

// DetailLines are the detail pane's lines for a selection ("node:<id>",
// "edge:<id>"): plain text, never interpreted.
func (g Graph) DetailLines(sel string) []string {
	switch {
	case strings.HasPrefix(sel, "node:"):
		if n, ok := g.Node(strings.TrimPrefix(sel, "node:")); ok {
			return n.Detail
		}
	case strings.HasPrefix(sel, "edge:"):
		id := strings.TrimPrefix(sel, "edge:")
		for _, e := range g.Edges {
			if e.ID == id {
				return []string{"Edge " + e.Label}
			}
		}
	}
	return nil
}

// egressEdge is the edge from a network to the Internet, and how it is drawn.
func egressEdge(nw engine.Network, e Egress) TopoEdge {
	te := TopoEdge{ID: "e:" + nw.Name, From: "n:" + nw.Name, To: internetNodeID}
	switch {
	case e.Policy == "gateway":
		te.Label = "egress via gateway"
		if e.GatewayKind != "" {
			te.Label = "egress via " + e.GatewayKind + " gateway"
		}
		if e.Operator != "" {
			te.Label += " (" + e.Operator + ")"
		}
		if e.Allowed > 0 {
			te.Label += fmt.Sprintf(", %d hosts allowed", e.Allowed)
		}
		if e.GatewayState != "" && e.GatewayState != "up" && e.GatewayState != "running" && e.GatewayState != "ready" {
			te.State = EdgeIdle
			te.Label += " (" + e.GatewayState + ")"
		}
	case e.Policy == "unknown":
		te.State, te.Label = EdgeIdle, "egress unknown"
	case e.Policy == "internal" || nw.Internal:
		te.State, te.Label = EdgeDenied, "internal only"
	case e.Policy == "none":
		te.State, te.Label = EdgeDenied, "no egress"
	default:
		te.State, te.Label = EdgeDenied, "no egress reported"
	}
	if e.Reason != "" && te.State != EdgeNormal {
		te.Label += ": " + e.Reason
	}
	return te
}

// Topology is the Topology view's viewmodel: the graph, rebuilt on every
// refresh, and what is selected in it.
type Topology struct {
	app   *App
	graph Graph
	// Revision counts rebuilds (the view redraws on it).
	Revision *mvvm.Observable[int]
	// Selected is "node:<id>", "edge:<id>" or "".
	Selected *mvvm.Observable[string]

	importFn func()
	exportFn func(project string)
}

func newTopology(a *App) *Topology {
	return &Topology{app: a, Revision: mvvm.NewObservable(0), Selected: mvvm.NewObservable("")}
}

// SetCompose sets how the page imports and exports a compose file: imp opens
// the page's file picker, exp downloads the project's file. A page without
// them leaves the controls disabled.
func (t *Topology) SetCompose(imp func(), exp func(project string)) {
	t.importFn, t.exportFn = imp, exp
	t.Revision.Set(t.Revision.Get() + 1)
}

// CanImport and CanExport say whether the page provides the compose transfer.
func (t *Topology) CanImport() bool { return t.importFn != nil }

// CanExport says whether a compose export is possible for the selection.
func (t *Topology) CanExport() bool { return t.exportFn != nil && t.SelectedProject() != "" }

// Import asks the page to pick a compose file.
func (t *Topology) Import() {
	if t.importFn != nil {
		t.app.LastCommand.Set("wctr compose config -f <file>")
		t.importFn()
	}
}

// Export asks the page to download the selected project's compose file.
func (t *Topology) Export() {
	if p := t.SelectedProject(); p != "" && t.exportFn != nil {
		t.app.LastCommand.Set("wctr compose -p " + p + " config")
		t.exportFn(p)
	}
}

// SelectedNode is the node the selection names.
func (t *Topology) SelectedNode() (TopoNode, bool) {
	sel := t.Selected.Get()
	if !strings.HasPrefix(sel, "node:") {
		return TopoNode{}, false
	}
	return t.graph.Node(strings.TrimPrefix(sel, "node:"))
}

// SelectedProject is the compose project of the selected container ("" for a
// standalone container, a network or nothing).
func (t *Topology) SelectedProject() string {
	n, ok := t.SelectedNode()
	if !ok || n.Kind != TopoContainer || n.Cluster == standaloneCluster {
		return ""
	}
	return n.Cluster
}

// NodeMembers are the containers a node's sound action reaches: itself for a
// container, its attached containers for a network.
func (t *Topology) NodeMembers(n TopoNode) []string {
	switch n.Kind {
	case TopoContainer:
		return []string{n.Container}
	case TopoNetwork:
		return t.graph.NetworkMembers(n.Network)
	}
	return nil
}

// MuteSelected mutes the selected container, or every container of the
// selected network (or unmutes them when all are muted already).
func (t *Topology) MuteSelected() {
	if n, ok := t.SelectedNode(); ok {
		t.toggleGroup(t.NodeMembers(n))
	}
}

// ToggleSound is the speaker toggle of a node: a container's own mute, or the
// mute of every container on a network.
func (t *Topology) ToggleSound(nodeID string) {
	if n, ok := t.graph.Node(nodeID); ok {
		t.toggleGroup(t.NodeMembers(n))
	}
}

// MuteSelectedProject mutes the selected container's compose project.
func (t *Topology) MuteSelectedProject() {
	if p := t.SelectedProject(); p != "" {
		t.toggleGroup(t.graph.ProjectMembers(p))
	}
}

func (t *Topology) toggleGroup(ids []string) {
	if len(ids) > 0 {
		t.app.Sound.SetMuted(ids, !t.app.Sound.AllMuted(ids))
	}
}

// Detail is the detail pane's text for the selection.
func (t *Topology) Detail() []string { return t.graph.DetailLines(t.Selected.Get()) }

// Graph is the current graph.
func (t *Topology) Graph() Graph { return t.graph }

func (t *Topology) apply(cs []api.ContainerSummary, nets []engine.Network, egress map[string]Egress) {
	t.graph = BuildGraph(TopoInput{Containers: cs, Networks: nets, Egress: egress})
	t.Revision.Set(t.Revision.Get() + 1)
}
