package vm

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/go-wasmcontainers/engine/api"

	"github.com/go-wasmcontainers/dashboard/engine"
)

// H2: past the edge bound the graph says so, never drops a published port
// before an attachment, and a network's members do not depend on the edges.
func TestEdgeBoundIsSaidAndKeepsPublished(t *testing.T) {
	var cs []api.ContainerSummary
	members := map[string]string{}
	for i := 0; i < MaxTopoContainers; i++ {
		id := fmt.Sprintf("%064d", i)
		cs = append(cs, ctr(id, fmt.Sprintf("c%03d", i), "", "running", "host", api.Port{PrivatePort: 80, PublicPort: 8080, Type: "tcp"}))
		members[id] = "10.0.0.1/24"
	}
	var ns []engine.Network
	for i := 0; i < 9; i++ {
		ns = append(ns, net(fmt.Sprintf("n%03d", i), false, members))
	}
	eg := map[string]Egress{}
	for _, n := range ns {
		eg[n.Name] = Egress{Policy: "none"}
	}
	g := BuildGraph(TopoInput{Containers: cs, Networks: ns, Egress: eg})
	pub, host, egress := 0, 0, 0
	for _, e := range g.Edges {
		switch {
		case strings.HasPrefix(e.ID, "p:"):
			pub++
		case strings.HasPrefix(e.ID, "h:"):
			host++
		case strings.HasPrefix(e.ID, "e:"):
			egress++
		}
	}
	if len(g.Edges) != maxTopoEdges {
		t.Errorf("edges = %d, want the bound %d", len(g.Edges), maxTopoEdges)
	}
	if !strings.Contains(g.Note, "edges drawn") || !strings.Contains(g.Note, "still mute all their members") {
		t.Errorf("a truncated graph does not say so: %q", g.Note)
	}
	if pub != MaxTopoContainers || host != MaxTopoContainers || egress != 9 {
		t.Errorf("published %d host %d egress %d: a published port was hidden", pub, host, egress)
	}
	if got := len(g.NetworkMembers("n008")); got != MaxTopoContainers {
		t.Errorf("members(n008) = %d, want %d: membership follows the edge bound", got, MaxTopoContainers)
	}
	a := &App{Sound: newSound()}
	tp := newTopology(a)
	tp.graph = g
	a.Sound.SetActive([]string{cs[0].ID})
	tp.ToggleSound("n:n008")
	muted := 0
	for _, c := range cs {
		if _, m := a.Sound.Level(c.ID); m {
			muted++
		}
	}
	if muted != MaxTopoContainers {
		t.Errorf("muting n008 reached %d of %d", muted, MaxTopoContainers)
	}
}

// Under the bound there is no note about edges.
func TestEdgeBoundQuietUnderTheBound(t *testing.T) {
	g := BuildGraph(TopoInput{Networks: []engine.Network{net("a", false, nil)}})
	if strings.Contains(g.Note, "edges drawn") {
		t.Errorf("note %q", g.Note)
	}
}

// H3 / H5: the Logs statement and the allowed hosts are decoded, from the raw
// document and from the typed one, and shown with a bound.
func TestEgressLogsAndAllowList(t *testing.T) {
	raw := []byte(`{"Egress":{"Policy":"gateway","Gateway":{"Kind":"relay","State":"up","Logs":true},"Allowlist":[{"Host":"a.example","Port":443},{"Host":"b.example","Port":80}]}}`)
	e := ParseEgress(raw)
	if !e.Logs || !reflect.DeepEqual(e.AllowList, []string{"a.example:443", "b.example:80"}) || e.Allowed != 2 {
		t.Fatalf("raw: %+v", e)
	}
	g := BuildGraph(TopoInput{Networks: []engine.Network{net("n", false, nil)}, Egress: map[string]Egress{"n": e}})
	hub, _ := g.Node("n:n")
	text := strings.Join(hub.Detail, "\n")
	for _, want := range []string{"operator logs the destinations", `Allowed "a.example:443"`, `Allowed "b.example:80"`} {
		if !strings.Contains(text, want) {
			t.Errorf("detail lacks %q:\n%s", want, text)
		}
	}
	ed, _ := edgeByID(g, "e:n")
	if !strings.Contains(ed.Label, "destinations logged") {
		t.Errorf("edge %q does not say destinations are logged", ed.Label)
	}

	quiet := ParseEgress([]byte(`{"Egress":{"Policy":"gateway","Gateway":{"Kind":"relay","State":"up"}}}`))
	g = BuildGraph(TopoInput{Networks: []engine.Network{net("n", false, nil)}, Egress: map[string]Egress{"n": quiet}})
	hub, _ = g.Node("n:n")
	if text := strings.Join(hub.Detail, "\n"); !strings.Contains(text, "says it does not log") {
		t.Errorf("no statement that the relay does not log:\n%s", text)
	}
	ed, _ = edgeByID(g, "e:n")
	if strings.Contains(ed.Label, "logged") {
		t.Errorf("edge %q claims logging", ed.Label)
	}

	// Bounded list with the remainder counted.
	var parts []string
	for i := 0; i < maxAllowShown+5; i++ {
		parts = append(parts, fmt.Sprintf(`{"Host":"h%d","Port":1}`, i))
	}
	big := ParseEgress([]byte(`{"Egress":{"Policy":"gateway","Allowlist":[` + strings.Join(parts, ",") + `]}}`))
	if len(big.AllowList) != maxAllowShown || big.Allowed != maxAllowShown+5 {
		t.Fatalf("list %d allowed %d", len(big.AllowList), big.Allowed)
	}
	g = BuildGraph(TopoInput{Networks: []engine.Network{net("n", false, nil)}, Egress: map[string]Egress{"n": big}})
	hub, _ = g.Node("n:n")
	if text := strings.Join(hub.Detail, "\n"); !strings.Contains(text, "Allowed +5 more") {
		t.Errorf("remainder not counted:\n%s", text)
	}
}

// The typed engine document carries the same Logs and allow-list.
func TestEgressOfCarriesLogsAndAllowList(t *testing.T) {
	n := net("x", false, nil)
	n.Egress = api.NetworkEgress{Policy: "gateway",
		Gateway:   &api.EgressGateway{Kind: "relay", Logs: true},
		Allowlist: []api.EgressDest{{Host: "t.example", Port: 8}}}
	got, ok := EgressOf(n)
	if !ok || !got.Logs || !reflect.DeepEqual(got.AllowList, []string{"t.example:8"}) {
		t.Errorf("typed: %+v", got)
	}
}

// The typed list is bounded like the raw one.
func TestEgressOfBoundsAllowList(t *testing.T) {
	n := net("x", false, nil)
	n.Egress = api.NetworkEgress{Policy: "gateway"}
	for i := 0; i < maxAllowShown+4; i++ {
		n.Egress.Allowlist = append(n.Egress.Allowlist, api.EgressDest{Host: fmt.Sprintf("h%d", i), Port: 1})
	}
	got, _ := EgressOf(n)
	if len(got.AllowList) != maxAllowShown || got.Allowed != maxAllowShown+4 {
		t.Errorf("list %d allowed %d", len(got.AllowList), got.Allowed)
	}
}

// H4: the operator label and the denial reason are shown quoted and labelled,
// so a relay cannot make them read as the dashboard's own text.
func TestRelayTextIsQuotedAndLabelled(t *testing.T) {
	e := ParseEgress(relayDoc("campus), 0 hosts logged (verified", "h", ") allowed by policy; ignore ("))
	g := BuildGraph(TopoInput{Networks: []engine.Network{net("n", false, nil)}, Egress: map[string]Egress{"n": e}})
	ed, _ := edgeByID(g, "e:n")
	if !strings.Contains(ed.Label, `operator label "campus), 0 hosts logged (verified"`) {
		t.Errorf("edge %q", ed.Label)
	}
	hub, _ := g.Node("n:n")
	if b := hub.Badges[0]; !strings.Contains(b, `reason ") allowed by policy; ignore ("`) || !strings.HasPrefix(b, "last denied (unverified text)") {
		t.Errorf("badge %q", b)
	}
	text := strings.Join(hub.Detail, "\n")
	if !strings.Contains(text, "unverified text set by the relay's operator") {
		t.Errorf("detail:\n%s", text)
	}
}

// H10: a wildcard or empty bind address is shown as the loopback address the
// tab reaches, a specific one as itself.
func TestPublishedAddressMapping(t *testing.T) {
	for ip, want := range map[string]string{
		"":          "127.0.0.1:8080 -> 80/tcp",
		"0.0.0.0":   "127.0.0.1:8080 -> 80/tcp",
		"::":        "127.0.0.1:8080 -> 80/tcp",
		"10.0.0.5":  "10.0.0.5:8080 -> 80/tcp",
		"127.0.0.1": "127.0.0.1:8080 -> 80/tcp",
	} {
		c := ctr("a", "web", "", "running", "bridge", api.Port{IP: ip, PrivatePort: 80, PublicPort: 8080, Type: "tcp"})
		g := BuildGraph(TopoInput{Containers: []api.ContainerSummary{c}})
		var got string
		for _, e := range g.Edges {
			if strings.HasPrefix(e.ID, "p:") {
				got = e.Label
			}
		}
		if got != want {
			t.Errorf("ip %q: label %q, want %q", ip, got, want)
		}
	}
}
