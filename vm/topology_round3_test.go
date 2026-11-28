package vm

import (
	"strings"
	"testing"

	"github.com/go-wasmcontainers/dashboard/engine"
)

// The privacy line follows what Logs says, whatever Gateway.Kind is.
func TestPrivacyLineWithoutGatewayKind(t *testing.T) {
	e := ParseEgress([]byte(`{"Egress":{"Policy":"gateway","Gateway":{"State":"up","Logs":true}}}`))
	g := BuildGraph(TopoInput{Networks: []engine.Network{net("n", false, nil)}, Egress: map[string]Egress{"n": e}})
	hub, _ := g.Node("n:n")
	if text := strings.Join(hub.Detail, "\n"); !strings.Contains(text, "operator logs the destinations") {
		t.Errorf("logging hidden when Kind is empty:\n%s", text)
	}
}

// One mistyped field does not discard the others.
func TestParseEgressFieldsDecodeIndependently(t *testing.T) {
	e := ParseEgress([]byte(`{"Egress":{"Policy":"gateway","Allowlist":"oops","Gateway":{"Kind":"relay","State":"up","Logs":true},"Reason":7}}`))
	if e.Policy != "gateway" || !e.Logs || e.GatewayKind != "relay" || e.Allowed != 0 {
		t.Errorf("a bad Allowlist/Reason dropped the rest: %+v", e)
	}
	e = ParseEgress([]byte(`{"Egress":{"Policy":"gateway","Gateway":"oops","Allowlist":[{"Host":"a","Port":1}]}}`))
	if e.Policy != "gateway" || e.Allowed != 1 || e.GatewayKind != "" {
		t.Errorf("a bad Gateway dropped the rest: %+v", e)
	}
}

// "destinations logged" comes before the operator's label, which a long label
// must not push out of the text.
func TestEdgeLabelPutsLoggedBeforeOperator(t *testing.T) {
	e := Egress{Policy: "gateway", GatewayKind: "relay", GatewayState: "up", Logs: true, Operator: strings.Repeat("x", 48)}
	g := BuildGraph(TopoInput{Networks: []engine.Network{net("n", false, nil)}, Egress: map[string]Egress{"n": e}})
	ed, _ := edgeByID(g, "e:n")
	i, j := strings.Index(ed.Label, "destinations logged"), strings.Index(ed.Label, "operator label")
	if i < 0 || j < 0 || i > j {
		t.Errorf("label %q", ed.Label)
	}
}
