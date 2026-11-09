package ui

import (
	"strings"
	"testing"

	"github.com/go-widgets/toolkit"
	"github.com/go-widgets/webcanvas"
)

// Ctrl+Shift+V in the focused terminal pastes the toolkit's clipboard as
// input, the way the page delivers it: the modifiers, then KeyDown("V").
// (The playground's e2e saw the key arrive with the clipboard filled and
// nothing typed.)
func TestTerminalPasteShortcut(t *testing.T) {
	defer toolkit.SetClipboard(nil)
	r := newRig(t)
	r.until("engine", func(p Probe) bool { return strings.HasPrefix(p.Status[0], "engine test") })
	r.s.KeyDown("F6")
	r.s.Modifiers(webcanvas.Modifiers{Alt: true})
	r.s.KeyDown("5")
	r.s.Modifiers(webcanvas.Modifiers{})
	r.click("terminal")

	toolkit.SetClipboardText("help")
	r.s.Modifiers(webcanvas.Modifiers{Ctrl: true, Shift: true})
	r.s.KeyDown("V")
	r.s.Modifiers(webcanvas.Modifiers{})
	r.until("the pasted word on the prompt", func(p Probe) bool { return strings.Contains(p.Terminal, "$ help") })
	r.s.KeyDown("Enter")
	r.until("help's output", func(p Probe) bool { return strings.Contains(p.Terminal, "This terminal runs wctr") })
}
