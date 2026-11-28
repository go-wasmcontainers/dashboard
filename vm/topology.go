package vm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

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

// The ids of the two gateway nodes, for a view that draws each its own way.
const (
	HostNodeID     = hostNodeID
	InternetNodeID = internetNodeID
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
	// members are the containers attached to each network, by network name:
	// kept apart from the edges, so the bound on edges never changes who is a member.
	members map[string][]string
}

// NetworkMembers are the full IDs of the containers attached to a network.
func (g Graph) NetworkMembers(network string) []string {
	out := append([]string(nil), g.members[network]...)
	sort.Strings(out)
	return out
}

// edgeRank orders what is kept when the edges pass maxTopoEdges: the way out
// first, then what comes in (published ports, host network), then attachments.
func edgeRank(id string) int {
	switch {
	case strings.HasPrefix(id, "e:"):
		return 0
	case strings.HasPrefix(id, "p:"), strings.HasPrefix(id, "h:"):
		return 1
	}
	return 2
}

// boundEdges keeps at most maxTopoEdges edges, by edgeRank and then in order,
// and says in a note how many it left out of each kind.
func boundEdges(edges []TopoEdge) ([]TopoEdge, string) {
	if len(edges) <= maxTopoEdges {
		return edges, ""
	}
	var left [3]int
	budget := maxTopoEdges
	keep := make([]bool, len(edges))
	for rank := 0; rank < 3; rank++ {
		for i, e := range edges {
			if edgeRank(e.ID) != rank {
				continue
			}
			if budget > 0 {
				keep[i] = true
				budget--
			} else {
				left[rank]++
			}
		}
	}
	out := make([]TopoEdge, 0, maxTopoEdges)
	for i, e := range edges {
		if keep[i] {
			out = append(out, e)
		}
	}
	var parts []string
	for rank, what := range []string{"egress", "published-port or host-network", "attachment"} {
		if left[rank] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s edges", left[rank], what))
		}
	}
	return out, fmt.Sprintf("%d of %d edges drawn (%s not drawn); networks still mute all their members", len(out), len(edges), strings.Join(parts, ", "))
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
	// Allowed is how many host:port pairs the allowlist holds; AllowList is the
	// first maxAllowShown of them, as "host:port" (cleaned, bounded).
	Allowed   int
	AllowList []string
	// Logs is the gateway's own statement that its operator logs destinations.
	// The relay's retention is not reported by the engine.
	Logs bool
	// LogsKnown is whether the gateway made that statement in a form that could
	// be read: when it did not, the view says nothing of logging.
	LogsKnown bool
	// Operator is the label the relay's operator set; DeniedHost and
	// DeniedReason are the last destination the relay refused. All three come
	// from the relay, a party outside the engine: they are plain text, cleaned
	// and bounded to maxRelayText runes, and nothing of them is interpreted.
	Operator, DeniedHost, DeniedReason string
}

// maxRelayText bounds each string the relay supplies (operator, denial).
const maxRelayText = 48

// maxAllowShown bounds the allowlist entries the view lists.
const maxAllowShown = 16

// ParseEgress decodes the Egress object of a network inspect document. It
// never fails: a document that is empty, oversized, not JSON, or without an
// Egress object yields the zero Egress ("no egress reported"). Every string is
// cleaned for display.
func ParseEgress(raw []byte) Egress {
	if len(raw) == 0 || len(raw) > maxEgressJSON {
		return Egress{}
	}
	top, ok := jsonFields(raw)
	if !ok || !json.Valid(raw) {
		return Egress{}
	}
	// Keys match without regard to case, and a key given twice does not hide the
	// one that decodes. Each field is decoded on its own: one of the wrong type
	// is dropped, the others are kept.
	f, ok := firstObject(top["egress"])
	if !ok {
		return Egress{}
	}
	policy, _ := first[string](f["policy"])
	reason, _ := first[string](f["reason"])
	allowlist, _ := first[[]struct {
		Host string `json:"Host"`
		Port int    `json:"Port"`
	}](f["allowlist"])
	counters, _ := firstObject(f["counters"])
	d, _ := first[*struct {
		Host   string `json:"Host"`
		Reason string `json:"Reason"`
	}](counters["lastdenied"])
	in := egressFields{policy: policy, reason: reason, allowed: len(allowlist)}
	if g, ok := firstObject(f["gateway"]); ok {
		in.hasGateway = true
		in.gwKind, _ = first[string](g["kind"])
		in.gwOperator, _ = first[string](g["operator"])
		in.gwState, _ = first[string](g["state"])
		// Logs on its own: a Logs that is not a bool is not a statement.
		in.logs, in.logsKnown = first[bool](g["logs"])
	}
	if d != nil {
		in.hasDenied, in.deniedHost, in.deniedReason = true, d.Host, d.Reason
	}
	for i, a := range allowlist {
		if i >= maxAllowShown {
			break
		}
		in.allow = append(in.allow, fmt.Sprintf("%s:%d", Clean(a.Host, 64), a.Port))
	}
	return in.build()
}

// jsonFields splits a JSON object into its members by lower-cased key, in
// document order, so a key repeated or spelled in another case keeps every
// value. ok is false when raw is not an object. A document that breaks off
// keeps what was read before.
func jsonFields(raw []byte) (map[string][]json.RawMessage, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, false
	}
	out := map[string][]json.RawMessage{}
	for dec.More() {
		k, err := dec.Token()
		key, isKey := k.(string)
		if err != nil || !isKey {
			break
		}
		var v json.RawMessage
		if dec.Decode(&v) != nil {
			break
		}
		key = strings.ToLower(key)
		out[key] = append(out[key], v)
	}
	return out, true
}

// firstObject is the fields of the first of the candidates that is an object.
func firstObject(c []json.RawMessage) (map[string][]json.RawMessage, bool) {
	for _, r := range c {
		if f, ok := jsonFields(r); ok {
			return f, true
		}
	}
	return nil, false
}

// first decodes the first of the candidates that is a T and not null.
func first[T any](c []json.RawMessage) (T, bool) {
	for _, r := range c {
		if string(bytes.TrimSpace(r)) == "null" {
			continue
		}
		var v T
		if json.Unmarshal(r, &v) == nil {
			return v, true
		}
	}
	var zero T
	return zero, false
}

// EgressOf is the Egress of a network the engine described with a typed
// Egress field (engine v0.52.0 and later): the same cleaning and bounds as
// ParseEgress, whatever the source. ok is false when the engine reported none
// (an older engine leaves Policy ""): the caller then falls back to the raw
// inspect document.
func EgressOf(n engine.Network) (e Egress, ok bool) {
	t := n.Egress
	if t.Policy == "" {
		return Egress{}, false
	}
	in := egressFields{policy: t.Policy, reason: t.Reason, allowed: len(t.Allowlist)}
	if g := t.Gateway; g != nil {
		in.hasGateway, in.gwKind, in.gwOperator, in.gwState, in.logs, in.logsKnown = true, g.Kind, g.Operator, g.State, g.Logs, true
	}
	if d := t.Counters.LastDenied; d != nil {
		in.hasDenied, in.deniedHost, in.deniedReason = true, d.Host, d.Reason
	}
	for i, a := range t.Allowlist {
		if i >= maxAllowShown {
			break
		}
		in.allow = append(in.allow, fmt.Sprintf("%s:%d", Clean(a.Host, 64), a.Port))
	}
	return in.build(), true
}

// egressFields is the one shape both decoders fill, so one function cleans it.
type egressFields struct {
	policy, reason              string
	allowed                     int
	hasGateway                  bool
	gwKind, gwOperator, gwState string
	hasDenied                   bool
	deniedHost, deniedReason    string
	logs, logsKnown             bool
	allow                       []string
}

func (in egressFields) build() Egress {
	e := Egress{Policy: Clean(strings.ToLower(in.policy), 16), Reason: Clean(in.reason, 120), Allowed: in.allowed, AllowList: in.allow, Logs: in.logs, LogsKnown: in.logsKnown}
	if in.hasGateway {
		e.GatewayKind, e.GatewayState = Clean(in.gwKind, 32), Clean(strings.ToLower(in.gwState), 32)
		e.Operator = RelayText(in.gwOperator, maxRelayText)
	}
	if in.hasDenied {
		e.DeniedHost, e.DeniedReason = RelayText(in.deniedHost, maxRelayText), RelayText(in.deniedReason, maxRelayText)
		if e.DeniedHost == "" {
			e.DeniedReason = "" // a denial with no visible host says nothing
		}
	}
	return e
}

// maxMarks is how many combining marks one base character keeps.
const maxMarks = 2

// invisibleFiller reports a character that draws nothing but is not a format
// character (Clean drops those): Hangul fillers, the braille blank, and the
// variation selectors, which only change how a neighbour looks.
func invisibleFiller(r rune) bool {
	switch {
	case r == 0x115F, r == 0x1160, r == 0x3164, r == 0xFFA0, r == 0x2800, r == 0x180E:
		return true
	case r >= 0xFE00 && r <= 0xFE0F, r >= 0xE0100 && r <= 0xE01EF:
		return true
	}
	return false
}

// RelayText cleans a string the relay supplied (operator, denial) for display
// as one line of plain text: Clean's rules, then no-break and other spaces
// become a plain space, invisible fillers and variation selectors are dropped,
// at most maxMarks combining marks stay on a base character (a mark with no
// base goes), and the result is bounded to max runes. A string with nothing
// visible left (only spaces, fillers, marks) is "": absent, not a blank label.
// Homoglyphs cannot be told from the real letters; callers that show the text
// as an identifier escape it (see quoteASCII) and say it is unverified.
func RelayText(s string, max int) string {
	var b strings.Builder
	marks, n := 0, 0
	space := false
	for _, r := range Clean(s, 1<<20) {
		switch {
		case invisibleFiller(r):
			continue
		case unicode.Is(unicode.Zs, r) || r == ' ':
			if b.Len() > 0 {
				space = true
			}
			marks = 0
			continue
		case unicode.IsMark(r):
			if marks >= maxMarks || b.Len() == 0 || space {
				continue
			}
			marks++
		default:
			marks = 0
		}
		if space {
			if n == max {
				b.WriteString("…")
				break
			}
			b.WriteByte(' ')
			space = false
			n++
		}
		if n == max {
			b.WriteString("…")
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String() // nothing visible leaves the builder empty: spaces, fillers and baseless marks write nothing
}

// quoteASCII shows relay text as an escaped literal: anything that is not
// printable ASCII appears as a \u escape, so a look-alike letter is visible.
func quoteASCII(s string) string { return strconv.QuoteToASCII(s) }

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

	addEdge := func(e TopoEdge) { g.Edges = append(g.Edges, e) }
	g.members = map[string][]string{}

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
			g.members[nw.Name] = append(g.members[nw.Name], x.sum.ID)
			x.nets = append(x.nets, strings.TrimSpace(Clean(nw.Name, 64)+" "+Clean(nw.Containers[id].IPv4Address, 64)))
			addEdge(TopoEdge{ID: "a:" + nw.Name + ":" + x.sum.ID, From: x.node.ID, To: hub.ID, Label: Clean(nw.Containers[id].IPv4Address, 64)})
		}
		eg := in.Egress[nw.Name]
		if eg.DeniedHost != "" {
			d := "last denied (unverified text) " + quoteASCII(eg.DeniedHost)
			if eg.DeniedReason != "" {
				d += " reason " + quoteASCII(eg.DeniedReason)
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
	var edgeNote string
	g.Edges, edgeNote = boundEdges(g.Edges)
	if edgeNote != "" {
		notes = append(notes, edgeNote)
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
		out = append(out, "Operator label (unverified text set by the relay's operator) "+quoteASCII(e.Operator))
	}
	if e.Logs {
		out = append(out, "Privacy: the relay's operator logs the destinations of this network (retention not reported)")
	} else if e.GatewayKind != "" && e.LogsKnown {
		out = append(out, "Privacy: the relay says it does not log destinations (its own statement, not checked)")
	}
	for _, a := range e.AllowList {
		out = append(out, "Allowed "+quoteASCII(a))
	}
	if e.Allowed > len(e.AllowList) {
		out = append(out, fmt.Sprintf("Allowed +%d more", e.Allowed-len(e.AllowList)))
	}
	if e.DeniedHost != "" {
		d := "Last denied (unverified text sent by the relay, not a checked host name) " + quoteASCII(e.DeniedHost)
		if e.DeniedReason != "" {
			d += " reason " + quoteASCII(e.DeniedReason)
		}
		out = append(out, d)
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
		if e.Logs {
			te.Label += ", destinations logged"
		}
		if e.Operator != "" {
			te.Label += " (operator label " + quoteASCII(e.Operator) + ")"
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
	if e.Logs && e.Policy != "gateway" {
		te.Label += ", destinations logged"
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

	// Status is the last compose import/export refusal or error ("" when none);
	// Preview is the output of the last command that ran (the `compose config`).
	Status, Preview *mvvm.Observable[string]

	importFn func()
	exportFn func(project string)
	imported string // the project of the current import
}

func newTopology(a *App) *Topology {
	return &Topology{app: a, Revision: mvvm.NewObservable(0), Selected: mvvm.NewObservable(""),
		Status: mvvm.NewObservable(""), Preview: mvvm.NewObservable("")}
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

// CanExport says whether a compose export is possible for the selection: only
// the project loaded by the current import can be exported, because the files
// the page holds are that project's, whatever else runs.
func (t *Topology) CanExport() bool {
	p := t.SelectedProject()
	return t.exportFn != nil && p != "" && p == t.imported
}

// Import asks the page to pick a compose file.
func (t *Topology) Import() {
	if t.importFn != nil {
		t.importFn()
	}
}

// Export asks the page to download the selected project's compose file; it
// says why not when the selection is not the imported project.
func (t *Topology) Export() {
	if t.exportFn == nil {
		return
	}
	if !t.CanExport() {
		t.ComposeFailed("export: select a container of the imported project" + t.importedHint())
		return
	}
	t.exportFn(t.SelectedProject())
}

func (t *Topology) importedHint() string {
	if t.imported == "" {
		return " (nothing is imported yet)"
	}
	return " " + quoteASCII(t.imported)
}

// ComposeImported records the project the current import loaded ("" when the
// files name none): the only one Export serves.
func (t *Topology) ComposeImported(project string) {
	t.imported = Clean(project, 64)
	t.Status.Set("")
	t.Revision.Set(t.Revision.Get() + 1)
}

// ComposeFailed shows an import or export refusal or error.
func (t *Topology) ComposeFailed(msg string) {
	t.Status.Set(Clean(msg, 300))
	t.Revision.Set(t.Revision.Get() + 1)
}

// ComposeDone records a wctr command the page really ran (the one the
// dashboard shows as the last command) and its output as a bounded preview.
func (t *Topology) ComposeDone(args []string, output string) {
	t.app.LastCommand.Set(Line(args...))
	t.Status.Set("")
	t.Preview.Set(previewText(output))
	t.Revision.Set(t.Revision.Get() + 1)
}

// previewText bounds a command's output for display: 40 lines of 200 runes.
func previewText(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) > 40 {
		lines = append(lines[:40], "…")
	}
	for i, l := range lines {
		lines[i] = Clean(l, 200)
	}
	return strings.Join(lines, "\n")
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
