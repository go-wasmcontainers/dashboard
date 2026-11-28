package vm

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/go-wasmcontainers/dashboard/engine"
)

func gwGraph(raw string) (Egress, TopoNode, TopoEdge) {
	e := ParseEgress([]byte(raw))
	g := BuildGraph(TopoInput{Networks: []engine.Network{net("class", false, nil)}, Egress: map[string]Egress{"class": e}})
	n, _ := g.Node("n:class")
	ed, _ := edgeByID(g, "e:class")
	return e, n, ed
}

// #34 item 1: Logs is decoded on its own. A value that is not a bool is not
// "does not log": the privacy line says nothing, and the other fields stay.
func TestEgressLogsUnreadableSaysNothing(t *testing.T) {
	for name, logs := range map[string]string{"string": `"true"`, "number": `1`, "null": `null`, "object": `{}`, "absent": ``} {
		field := ""
		if logs != "" {
			field = `,"Logs":` + logs
		}
		e, n, ed := gwGraph(`{"Egress":{"Policy":"gateway","Gateway":{"Kind":"relay","State":"up"` + field + `}}}`)
		d := strings.Join(n.Detail, "\n")
		if strings.Contains(d, "does not log") || strings.Contains(d, "logs the destinations") || strings.Contains(ed.Label, "logged") {
			t.Errorf("%s: a privacy statement from an unreadable Logs:\n%s\n%s", name, d, ed.Label)
		}
		if e.GatewayKind != "relay" || e.GatewayState != "up" {
			t.Errorf("%s: the other gateway fields were lost: %+v", name, e)
		}
	}
	// A readable false is a statement; a readable true too.
	_, n, _ := gwGraph(`{"Egress":{"Policy":"gateway","Gateway":{"Kind":"relay","Logs":false}}}`)
	if !strings.Contains(strings.Join(n.Detail, "\n"), "does not log") {
		t.Error("Logs:false lost its statement")
	}
}

// #34 item 2: keys match without regard to case, and a duplicated key does not
// hide the one that decodes.
func TestEgressKeysCaseAndDuplicates(t *testing.T) {
	for name, raw := range map[string]string{
		"gateway lower": `{"Egress":{"policy":"gateway","gateway":{"kind":"relay","logs":true}}}`,
		"egress lower":  `{"egress":{"Policy":"gateway","Gateway":{"Kind":"relay","Logs":true}}}`,
		"gateway twice": `{"Egress":{"Policy":"gateway","Gateway":{"Kind":"relay","Logs":true},"Gateway":7}}`,
		"twice reverse": `{"Egress":{"Policy":"gateway","Gateway":7,"GATEWAY":{"Kind":"relay","Logs":true}}}`,
	} {
		e := ParseEgress([]byte(raw))
		if e.Policy != "gateway" || e.GatewayKind != "relay" || !e.Logs {
			t.Errorf("%s: %+v", name, e)
		}
	}
}

// #34 item 3: the edge says destinations are logged whatever the policy.
func TestEdgeSaysLoggedWhateverThePolicy(t *testing.T) {
	_, _, ed := gwGraph(`{"Egress":{"Policy":"none","Gateway":{"Kind":"relay","Logs":true}}}`)
	if !strings.Contains(ed.Label, "destinations logged") {
		t.Errorf("edge %q", ed.Label)
	}
}

// #34 item 4: a group refusal is one short line, and the reason comes first.
func TestRefusalTextBounded(t *testing.T) {
	s := newSound()
	s.SetAudio(func(string, float64, bool) bool { return true })
	for i := 0; i < MaxSoundKeys; i++ {
		s.SetVolume(fmt.Sprintf("%064d", i), 50)
	}
	ids := make([]string, 256)
	for i := range ids {
		ids[i] = fmt.Sprintf("m%063d", i)
	}
	s.SetMuted(ids, true)
	got := s.Refused.Get()
	if n := utf8.RuneCountInString(got); n > 200 || n == 0 {
		t.Errorf("refusal of %d runes: %q", n, got)
	}
	if at := strings.Index(got, "already have"); at < 0 || at > 80 {
		t.Errorf("the reason is not first (byte %d): %q", at, got)
	}
	if !strings.Contains(got, "256") {
		t.Errorf("the count of refused members is missing: %q", got)
	}
}

// #34 item 5: unmuting a container that has no level, with the store full, is
// a no-op and is not reported as refused.
func TestNoopUnmuteNotRefused(t *testing.T) {
	s := newSound()
	s.SetAudio(func(string, float64, bool) bool { return true })
	for i := 0; i < MaxSoundKeys; i++ {
		s.SetMuted([]string{fmt.Sprintf("%064d", i)}, true)
	}
	s.SetMuted([]string{"fresh"}, false)
	if got := s.Refused.Get(); got != "" {
		t.Errorf("refused: %q", got)
	}
	if len(s.levels) != MaxSoundKeys {
		t.Errorf("%d levels kept", len(s.levels))
	}
	s.SetMuted([]string{"fresh"}, true) // a real change is still refused
	if s.Refused.Get() == "" {
		t.Error("a real mute past the bound was not refused")
	}
}
