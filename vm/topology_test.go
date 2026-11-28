package vm

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/go-wasmcontainers/engine/api"

	"github.com/go-wasmcontainers/dashboard/engine"
)

func ctr(id, name, project, state, mode string, ports ...api.Port) api.ContainerSummary {
	c := api.ContainerSummary{ID: id, Names: []string{"/" + name}, State: state, Labels: map[string]string{}}
	if project != "" {
		c.Labels[composeProjectLabel] = project
	}
	c.HostConfig.NetworkMode = mode
	c.Ports = []any{}
	for _, p := range ports {
		c.Ports = append(c.Ports, p)
	}
	return c
}

func net(name string, internal bool, members map[string]string) engine.Network {
	n := engine.Network{Name: name, Internal: internal, Containers: map[string]api.NetworkEndpoint{}}
	for id, ip := range members {
		n.Containers[id] = api.NetworkEndpoint{IPv4Address: ip}
	}
	return n
}

func edgeByID(g Graph, id string) (TopoEdge, bool) {
	for _, e := range g.Edges {
		if e.ID == id {
			return e, true
		}
	}
	return TopoEdge{}, false
}

// A multi-homed container sits on two hubs; networks are hubs and
// attachments are edges; projects are clusters, standalone last.
func TestGraphMultiHomed(t *testing.T) {
	g := BuildGraph(TopoInput{
		Containers: []api.ContainerSummary{
			ctr("aaaa1111", "web", "shop", "running", "front"),
			ctr("bbbb2222", "db", "shop", "running", "back"),
			ctr("cccc3333", "tool", "", "running", "bridge"),
		},
		Networks: []engine.Network{
			net("front", false, map[string]string{"aaaa1111": "10.0.0.2/24"}),
			net("back", true, map[string]string{"aaaa1111": "10.1.0.2/24", "bbbb2222": "10.1.0.3/24"}),
		},
	})
	if !reflect.DeepEqual(g.Clusters, []string{"shop", standaloneCluster}) {
		t.Errorf("clusters = %v", g.Clusters)
	}
	if got := g.NetworkMembers("back"); !reflect.DeepEqual(got, []string{"aaaa1111", "bbbb2222"}) {
		t.Errorf("back members = %v", got)
	}
	if got := g.NetworkMembers("front"); !reflect.DeepEqual(got, []string{"aaaa1111"}) {
		t.Errorf("front members = %v", got)
	}
	if got := g.ProjectMembers("shop"); !reflect.DeepEqual(got, []string{"aaaa1111", "bbbb2222"}) {
		t.Errorf("shop members = %v", got)
	}
	if got := g.ProjectMembers(standaloneCluster); !reflect.DeepEqual(got, []string{"cccc3333"}) {
		t.Errorf("standalone = %v", got)
	}
	e, ok := edgeByID(g, "a:back:aaaa1111")
	if !ok || e.From != "c:aaaa1111" || e.To != "n:back" || e.Label != "10.1.0.2/24" {
		t.Errorf("attachment = %+v %v", e, ok)
	}
	if n, ok := g.Node("n:back"); !ok || n.Kind != TopoNetwork {
		t.Errorf("hub = %+v %v", n, ok)
	}
}

// Egress: absent renders as "no egress", never a failure; gateway up is
// normal, down is idle; internal and none are denied.
func TestGraphEgress(t *testing.T) {
	in := TopoInput{
		Networks: []engine.Network{
			net("open", false, nil), net("closed", false, nil), net("sealed", true, nil),
			net("down", false, nil), net("mystery", false, nil), net("absent", false, nil),
		},
		Egress: map[string]Egress{
			"open":    {Policy: "gateway", GatewayKind: "relay", GatewayState: "up", Allowed: 3},
			"closed":  {Policy: "none"},
			"sealed":  {Policy: "internal", Reason: "internal network"},
			"down":    {Policy: "gateway", GatewayKind: "relay", GatewayState: "down"},
			"mystery": {Policy: "unknown"},
		},
	}
	g := BuildGraph(in)
	want := map[string]struct {
		state TopoEdgeState
		label string
	}{
		"e:open":    {EdgeNormal, "egress via relay gateway, 3 hosts allowed"},
		"e:closed":  {EdgeDenied, "no egress"},
		"e:sealed":  {EdgeDenied, "internal only: internal network"},
		"e:down":    {EdgeIdle, "egress via relay gateway (down)"},
		"e:mystery": {EdgeIdle, "egress unknown"},
		"e:absent":  {EdgeDenied, "no egress reported"},
	}
	for id, w := range want {
		e, ok := edgeByID(g, id)
		if !ok || e.State != w.state || e.Label != w.label || e.To != internetNodeID {
			t.Errorf("%s = %+v (found %v), want %+v", id, e, ok, w)
		}
	}
	if _, ok := g.Node(internetNodeID); !ok {
		t.Errorf("no Internet node")
	}
}

// Published ports come in from "This tab"; exposed-only ports are badges; the
// empty host IP reads 127.0.0.1; host mode attaches to "This tab".
func TestGraphPorts(t *testing.T) {
	g := BuildGraph(TopoInput{
		Containers: []api.ContainerSummary{
			ctr("aaaa1111", "web", "", "running", "bridge",
				api.Port{PrivatePort: 80, PublicPort: 8080, Type: "tcp"},
				api.Port{IP: "127.0.0.1", PrivatePort: 443, PublicPort: 8443, Type: "tcp"},
				api.Port{PrivatePort: 9000, Type: "tcp"}),
			ctr("bbbb2222", "mon", "", "running", "host"),
		},
	})
	e, ok := edgeByID(g, "p:aaaa1111:80/tcp:8080")
	if !ok || e.From != hostNodeID || e.Label != "127.0.0.1:8080 -> 80/tcp" {
		t.Errorf("published = %+v %v", e, ok)
	}
	if _, ok := edgeByID(g, "h:bbbb2222"); !ok {
		t.Errorf("host network edge missing")
	}
	n, _ := g.Node("c:aaaa1111")
	if !reflect.DeepEqual(n.Badges, []string{"9000/tcp"}) {
		t.Errorf("badges = %v", n.Badges)
	}
	if h, ok := g.Node(hostNodeID); !ok || h.Label != "This tab" {
		t.Errorf("host node = %+v %v", h, ok)
	}
}

func TestGraphNoneAndEmpty(t *testing.T) {
	g := BuildGraph(TopoInput{Containers: []api.ContainerSummary{ctr("aaaa1111", "iso", "", "running", "none")}})
	if len(g.Edges) != 0 {
		t.Errorf("a container on no network has no edge: %+v", g.Edges)
	}
	if _, ok := g.Node(hostNodeID); ok {
		t.Errorf("no host node without a port or host mode")
	}
	if _, ok := g.Node(internetNodeID); ok {
		t.Errorf("no Internet node without a network")
	}
	if e := BuildGraph(TopoInput{}); len(e.Nodes) != 0 || len(e.Edges) != 0 {
		t.Errorf("empty input: %+v", e)
	}
}

// A network lists a container by short ID, or one that is not in the list.
func TestGraphAttachmentMatching(t *testing.T) {
	full := strings.Repeat("ab", 32)
	g := BuildGraph(TopoInput{
		Containers: []api.ContainerSummary{ctr(full, "web", "", "running", "bridge")},
		Networks:   []engine.Network{net("n", false, map[string]string{full[:12]: "10.0.0.2/24", "ghost": "10.0.0.3/24"})},
	})
	if got := g.NetworkMembers("n"); !reflect.DeepEqual(got, []string{full}) {
		t.Errorf("members = %v (the audio key is the FULL id)", got)
	}
}

func TestGraphBounds(t *testing.T) {
	var cs []api.ContainerSummary
	var ns []engine.Network
	members := map[string]string{}
	for i := 0; i < MaxTopoContainers+30; i++ {
		id := fmt.Sprintf("%064d", i)
		cs = append(cs, ctr(id, fmt.Sprintf("c%d", i), "", "running", "bridge"))
		members[id] = "10.0.0.1/24"
	}
	for i := 0; i < MaxTopoNetworks+10; i++ {
		ns = append(ns, net(fmt.Sprintf("n%03d", i), false, members))
	}
	g := BuildGraph(TopoInput{Containers: cs, Networks: ns})
	nodes := 0
	for _, n := range g.Nodes {
		if n.Kind != TopoGateway {
			nodes++
		}
	}
	if nodes > MaxTopoContainers+MaxTopoNetworks || len(g.Edges) > maxTopoEdges || g.Note == "" {
		t.Errorf("nodes %d edges %d note %q", nodes, len(g.Edges), g.Note)
	}
}

func TestParseEgress(t *testing.T) {
	cases := map[string]Egress{
		``:                             {},
		`not json`:                     {},
		`{"Name":"x"}`:                 {},
		`{"Egress":null}`:              {},
		`{"Egress":{"Policy":"NONE"}}`: {Policy: "none"},
		`{"Egress":{"Policy":"gateway","Reason":"r","Gateway":{"Kind":"relay","State":"Up"},"Allowlist":[{"Host":"a","Port":1},{"Host":"b"}]}}`: {Policy: "gateway", Reason: "r", GatewayKind: "relay", GatewayState: "up", Allowed: 2},
		// The shape engine PR #95 serves: Operator and LastDenied come from
		// the relay, so they are kept as plain text only.
		`{"Egress":{"Policy":"gateway","Gateway":{"Kind":"relay","Operator":"<b>Lab</b>","State":"up"},"Counters":{"LastDenied":{"Host":"x.example.org","Reason":"sni","Time":"2026-11-28T10:00:00Z"}}}}`: {Policy: "gateway", GatewayKind: "relay", GatewayState: "up", Operator: "<b>Lab</b>", DeniedHost: "x.example.org", DeniedReason: "sni"},
	}
	for in, want := range cases {
		if got := ParseEgress([]byte(in)); got != want {
			t.Errorf("ParseEgress(%q) = %+v, want %+v", in, got, want)
		}
	}
	// Control characters and length are cleaned.
	e := ParseEgress([]byte(`{"Egress":{"Policy":"none","Reason":"` + strings.Repeat("x\\u001b", 500) + `"}}`))
	// Clean keeps max runes and adds one ellipsis.
	if strings.ContainsRune(e.Reason, 0x1b) || len([]rune(e.Reason)) > 121 {
		t.Errorf("reason not cleaned: %d runes", len([]rune(e.Reason)))
	}
	long := strings.Repeat("y\\u202e", 400)
	e = ParseEgress([]byte(`{"Egress":{"Policy":"gateway","Gateway":{"Operator":"` + long + `"},"Counters":{"LastDenied":{"Host":"` + long + `","Reason":"` + long + `"}}}}`))
	for name, s := range map[string]string{"operator": e.Operator, "host": e.DeniedHost, "reason": e.DeniedReason} {
		if strings.ContainsRune(s, 0x202e) || len([]rune(s)) > maxRelayText+1 {
			t.Errorf("%s not cleaned: %d runes", name, len([]rune(s)))
		}
	}
}

// The relay's strings reach the graph as plain, bounded text: the operator on
// the egress edge, the last refusal as a badge of the network hub.
func TestGraphEgressRelayText(t *testing.T) {
	g := BuildGraph(TopoInput{
		Networks: []engine.Network{net("class", false, nil)},
		Egress: map[string]Egress{"class": {Policy: "gateway", GatewayKind: "relay", GatewayState: "up",
			Operator: "Lab IT", Allowed: 1, DeniedHost: "x.example.org", DeniedReason: "sni"}},
	})
	e, _ := edgeByID(g, "e:class")
	if e.Label != "egress via relay gateway (Lab IT), 1 hosts allowed" || e.State != EdgeNormal {
		t.Errorf("edge = %+v", e)
	}
	n, _ := g.Node("n:class")
	if !reflect.DeepEqual(n.Badges, []string{"last denied x.example.org (sni)"}) {
		t.Errorf("hub badges = %v", n.Badges)
	}
}

// Bomb: an oversized inspect document is refused, not decoded.
func TestParseEgressBomb(t *testing.T) {
	big := `{"Egress":{"Policy":"gateway","Allowlist":[` + strings.Repeat(`{"Host":"a"},`, maxEgressJSON/10) + `{}]}}`
	if e := ParseEgress([]byte(big)); e != (Egress{}) {
		t.Errorf("an oversized document was decoded: %+v", e)
	}
}

func TestParsePorts(t *testing.T) {
	// A remote engine's list decodes to maps; bad entries are skipped.
	var list []any
	if err := json.Unmarshal([]byte(`[{"IP":"0.0.0.0","PrivatePort":80,"PublicPort":8080,"Type":"tcp"},{"PrivatePort":0},"x",{"PrivatePort":53,"Type":"udp"},{"PrivatePort":"bad"}]`), &list); err != nil {
		t.Fatal(err)
	}
	got := ParsePorts(list)
	want := []PortBinding{{IP: "0.0.0.0", Private: 80, Public: 8080, Proto: "tcp"}, {Private: 53, Proto: "udp"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParsePorts = %+v, want %+v", got, want)
	}
	many := make([]any, 1000)
	for i := range many {
		many[i] = api.Port{PrivatePort: uint16(i + 1)}
	}
	if n := len(ParsePorts(many)); n != 256 {
		t.Errorf("kept %d ports, want the bound 256", n)
	}
}

// The refresh builds the graph; an engine that serves no network inspect for
// egress leaves "no egress reported".
func TestTopologyFollowsRefresh(t *testing.T) {
	h := newHarness(t)
	h.f.set(func() {
		h.f.netErr = nil
		h.f.containers = []api.ContainerSummary{ctr("aaaa1111", "web", "", "running", "bridge")}
		h.f.networks = []engine.Network{net("class", false, map[string]string{"aaaa1111": "10.0.0.2/24"})}
	})
	h.refresh()
	g := h.app.Topology.Graph()
	if got := g.NetworkMembers("class"); !reflect.DeepEqual(got, []string{"aaaa1111"}) {
		t.Fatalf("members = %v", got)
	}
	if e, ok := edgeByID(g, "e:class"); !ok || e.Label != "no egress reported" {
		t.Errorf("egress edge = %+v %v", e, ok)
	}
}

func FuzzParseEgress(f *testing.F) {
	f.Add([]byte(`{"Egress":{"Policy":"gateway","Gateway":{"Kind":"relay","State":"up"},"Allowlist":[{"Host":"a","Port":1}]}}`))
	f.Add([]byte(`{"Egress":null}`))
	f.Add([]byte(`[`))
	f.Fuzz(func(t *testing.T, b []byte) {
		e := ParseEgress(b)
		if len([]rune(e.Policy)) > 17 || len([]rune(e.Reason)) > 121 || len([]rune(e.Operator)) > maxRelayText+1 ||
			len([]rune(e.DeniedHost)) > maxRelayText+1 || len([]rune(e.DeniedReason)) > maxRelayText+1 {
			t.Fatalf("unbounded fields: %+v", e)
		}
	})
}

func FuzzParsePorts(f *testing.F) {
	f.Add([]byte(`[{"IP":"0.0.0.0","PrivatePort":80,"PublicPort":8080,"Type":"tcp"}]`))
	f.Add([]byte(`[null,1,"a",{"PrivatePort":-1}]`))
	f.Fuzz(func(t *testing.T, b []byte) {
		var list []any
		if json.Unmarshal(b, &list) != nil {
			return
		}
		if n := len(ParsePorts(list)); n > 256 {
			t.Fatalf("%d ports", n)
		}
	})
}

// Nodes come out project by project (the order of the clusters), then by name.
func TestGraphNodeOrder(t *testing.T) {
	g := BuildGraph(TopoInput{Containers: []api.ContainerSummary{
		ctr("1111", "zeta", "", "running", "bridge"),
		ctr("2222", "beta", "shop", "running", "bridge"),
		ctr("3333", "alpha", "", "running", "bridge"),
		ctr("4444", "omega", "shop", "running", "bridge"),
	}})
	var got []string
	for _, n := range g.Nodes {
		got = append(got, n.Label)
	}
	if want := []string{"beta", "omega", "alpha", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// An exact ID wins over a short-ID match, even when two share the prefix.
func TestGraphExactIDBeatsPrefix(t *testing.T) {
	a := "abcdefabcdef" + strings.Repeat("0", 52)
	b := "abcdefabcdef" + strings.Repeat("1", 52)
	for i := 0; i < 20; i++ {
		g := BuildGraph(TopoInput{
			Containers: []api.ContainerSummary{ctr(a, "a", "", "running", "bridge"), ctr(b, "b", "", "running", "bridge")},
			Networks:   []engine.Network{net("n", false, map[string]string{b: "10.0.0.2/24"})},
		})
		if got := g.NetworkMembers("n"); !reflect.DeepEqual(got, []string{b}) {
			t.Fatalf("members = %v, want only b", got)
		}
	}
}

// A document that is not valid JSON yields nothing, even if a prefix decoded.
func TestParseEgressInvalidTail(t *testing.T) {
	if e := ParseEgress([]byte(`{"Egress":{"Policy":"none"}} trailing`)); e != (Egress{}) {
		t.Errorf("decoded an invalid document: %+v", e)
	}
}

// A topology with two projects: the sound and compose actions of the view.
func topoApp(t *testing.T) *App {
	t.Helper()
	h := newHarness(t)
	h.app.Topology.graph = BuildGraph(TopoInput{
		Containers: []api.ContainerSummary{
			ctr("aaaa1111", "web", "shop", "running", "front"),
			ctr("bbbb2222", "db", "shop", "running", "front"),
			ctr("cccc3333", "tool", "", "running", "front"),
		},
		Networks: []engine.Network{net("front", false, map[string]string{"aaaa1111": "10.0.0.2/24", "bbbb2222": "10.0.0.3/24", "cccc3333": "10.0.0.4/24"})},
	})
	h.app.Sound.SetActive([]string{"aaaa1111", "bbbb2222", "cccc3333"})
	return h.app
}

func TestTopologyMute(t *testing.T) {
	a := topoApp(t)
	muted := func(id string) bool { _, m := a.Sound.Level(id); return m }

	a.Topology.ToggleSound("c:aaaa1111")
	if !muted("aaaa1111") || muted("bbbb2222") {
		t.Error("a container's speaker must mute that container only")
	}
	a.Topology.Selected.Set("node:c:bbbb2222")
	a.Topology.MuteSelectedProject()
	if !muted("bbbb2222") || muted("cccc3333") {
		t.Error("the project mute must reach its containers only")
	}
	a.Topology.Selected.Set("node:n:front")
	a.Topology.MuteSelected() // not all muted: mutes every member
	if !muted("cccc3333") {
		t.Error("a network's mute must reach every member")
	}
	a.Topology.MuteSelected() // all muted: unmutes
	if muted("aaaa1111") || muted("cccc3333") {
		t.Error("a second network mute must unmute")
	}
	a.Topology.Selected.Set("")
	a.Topology.MuteSelected() // nothing selected: nothing happens
	a.Topology.ToggleSound("c:missing")
	a.Topology.MuteSelectedProject()
}

func TestTopologyDetailAndCompose(t *testing.T) {
	a := topoApp(t)
	a.Topology.Selected.Set("node:n:front")
	if d := a.Topology.Detail(); len(d) == 0 || !strings.HasPrefix(d[0], "Network front") {
		t.Errorf("hub detail = %v", d)
	}
	if got := a.Topology.Graph().DetailLines("node:nope"); got != nil {
		t.Errorf("unknown node: %v", got)
	}
	if got := a.Topology.Graph().DetailLines("edge:nope"); got != nil {
		t.Errorf("unknown edge: %v", got)
	}

	if a.Topology.CanImport() || a.Topology.CanExport() {
		t.Fatal("compose offered by a page without it")
	}
	a.Topology.Import()
	a.Topology.Export() // nothing to call: no panic
	var imported int
	var exported []string
	a.Topology.SetCompose(func() { imported++ }, func(p string) { exported = append(exported, p) })
	a.Topology.Selected.Set("node:c:cccc3333") // standalone: no project
	if !a.Topology.CanImport() || a.Topology.CanExport() {
		t.Error("export needs a compose project")
	}
	a.Topology.Export()
	a.Topology.Selected.Set("node:c:aaaa1111")
	a.Topology.Import()
	a.Topology.Export()
	if imported != 1 || len(exported) != 1 || exported[0] != "shop" {
		t.Errorf("imported %d, exported %v", imported, exported)
	}
	if a.LastCommand.Get() != "wctr compose -p shop config" {
		t.Errorf("last command = %q", a.LastCommand.Get())
	}
}

// The detail of an edge is its own label, not another edge's; a selection
// that is not a node names no node.
func TestTopologyEdgeSelection(t *testing.T) {
	a := topoApp(t)
	g := a.Topology.Graph()
	if got := g.DetailLines("edge:a:front:aaaa1111"); !reflect.DeepEqual(got, []string{"Edge 10.0.0.2/24"}) {
		t.Errorf("first edge = %v", got)
	}
	if got := g.DetailLines("edge:a:front:bbbb2222"); !reflect.DeepEqual(got, []string{"Edge 10.0.0.3/24"}) {
		t.Errorf("second edge = %v", got)
	}
	for _, sel := range []string{"edge:a:front:aaaa1111", "c:aaaa1111", "n:front", "nodes:c:aaaa1111", ""} {
		a.Topology.Selected.Set(sel)
		if n, ok := a.Topology.SelectedNode(); ok {
			t.Errorf("selection %q names node %+v", sel, n)
		}
	}
	a.Topology.Selected.Set("node:c:aaaa1111")
	if n, ok := a.Topology.SelectedNode(); !ok || n.Container != "aaaa1111" {
		t.Errorf("node selection = %+v %v", n, ok)
	}
}

// ParseEgress of anything that carries no Egress object is the zero value.
func TestParseEgressAbsent(t *testing.T) {
	for _, raw := range []string{`not json`, `{"x":1}`, `{"Egress":null}`, `[1]`, `{"Egress":`} {
		if e := ParseEgress([]byte(raw)); !reflect.DeepEqual(e, Egress{}) {
			t.Errorf("ParseEgress(%q) = %+v", raw, e)
		}
	}
	if e := ParseEgress([]byte(`{"Egress":{"Policy":"none"}}`)); e.Policy != "none" {
		t.Errorf("policy = %q", e.Policy)
	}
}

// TestTopologyCoverageGuards holds the bounds and refusals the other tests
// leave unexercised: an id-less container, more port badges than fit, a sound
// action on a gateway, a changed set of sounding containers of the same size,
// and the cap on inspected networks.
func TestTopologyCoverageGuards(t *testing.T) {
	g := BuildGraph(TopoInput{Containers: []api.ContainerSummary{ctr("", "ghost", "", "running", "bridge")}})
	if len(g.Nodes) != 0 {
		t.Errorf("a container without an id became a node: %+v", g.Nodes)
	}

	var ports []api.Port
	for i := 1; i <= maxTopoPorts+4; i++ {
		ports = append(ports, api.Port{PrivatePort: uint16(i), Type: "tcp"})
	}
	g = BuildGraph(TopoInput{Containers: []api.ContainerSummary{ctr("aaaa1111", "web", "", "running", "bridge", ports...)}})
	n, _ := g.Node("c:aaaa1111")
	if len(n.Badges) != maxTopoPorts+1 || n.Badges[maxTopoPorts] != "+4" {
		t.Errorf("badges = %v, want %d ports and +4", n.Badges, maxTopoPorts)
	}

	a := topoApp(t)
	a.Topology.graph = BuildGraph(TopoInput{Containers: []api.ContainerSummary{
		ctr("aaaa1111", "web", "", "running", "bridge", api.Port{PrivatePort: 80, PublicPort: 8080, Type: "tcp"}),
	}})
	if m := a.Topology.NodeMembers(TopoNode{Kind: TopoGateway}); m != nil {
		t.Errorf("a gateway has members %v", m)
	}
	a.Topology.ToggleSound(hostNodeID) // a gateway has no sound: nothing happens
	if _, muted := a.Sound.Level("aaaa1111"); muted {
		t.Error("a gateway's speaker muted a container")
	}

	a.Sound.SetActive([]string{"x"})
	a.Sound.SetActive([]string{"y"}) // same size, other member: it must change
	if a.Sound.Active("x") || !a.Sound.Active("y") {
		t.Error("SetActive kept the old set")
	}
}

func TestRefreshInspectsAtMostMaxNetworks(t *testing.T) {
	h := newHarness(t)
	var ns []engine.Network
	for i := 0; i < MaxTopoNetworks+3; i++ {
		ns = append(ns, net(fmt.Sprintf("n%03d", i), false, nil))
	}
	h.f.set(func() {
		h.f.netErr = nil
		h.f.networks = ns
	})
	h.refresh()
	if g := h.app.Topology.Graph(); g.Note == "" {
		t.Errorf("no note about the networks left out: %+v", g)
	}
}
