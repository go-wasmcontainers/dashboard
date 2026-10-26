package vm

import (
	"strings"
	"testing"
	"unicode"
)

// FuzzInspectTree feeds arbitrary inspect documents (they come from the
// engine, and carry container-provided strings): the tree stays bounded
// and no key or value can carry a control or format character.
func FuzzInspectTree(f *testing.F) {
	for _, s := range []string{`{"Name":"/web","Config":{"Env":["A=1"]}}`, `[1,[2,[3]]]`, `{"a\u001b":"‮"}`, `"x"`, `{`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, doc []byte) {
		nodes, err := InspectTree(doc)
		if err != nil {
			return
		}
		if len(nodes) > maxInspectNodes+1 {
			t.Fatalf("%d nodes", len(nodes))
		}
		for _, n := range nodes {
			if n.Depth > maxInspectDepth+1 {
				t.Fatalf("depth %d", n.Depth)
			}
			for _, s := range []string{n.Key, n.Value} {
				if i := strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }); i >= 0 {
					t.Fatalf("control character in %q", s)
				}
			}
		}
	})
}

// FuzzClean checks Clean's promise on arbitrary strings.
func FuzzClean(f *testing.F) {
	f.Add("web\x1b]52;c;x\x07‮", 10)
	f.Fuzz(func(t *testing.T, s string, n int) {
		n = n%300 + 1
		if n < 1 {
			n = 1
		}
		out := Clean(s, n)
		if strings.IndexFunc(out, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) >= 0 {
			t.Fatalf("Clean(%q) = %q", s, out)
		}
		if len([]rune(out)) > n+1 {
			t.Fatalf("Clean kept %d runes over %d", len([]rune(out)), n)
		}
	})
}
