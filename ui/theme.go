package ui

import "github.com/go-widgets/toolkit"

// The systems family's gradient (go-wasmcontainers/brand): #22CCE2 -> #0079A8.
var (
	brandLight = toolkit.RGB(0x22, 0xCC, 0xE2)
	brandDeep  = toolkit.RGB(0x00, 0x79, 0xA8)
)

// Light is the light theme: the deep end of the brand gradient as accent
// (4.9:1 on white, for focus rings and selection). Border is dark enough to
// delimit panels and tables: at least 3:1 against every ground it sits on
// (WCAG 2.2 SC 1.4.11, non-text contrast; asserted in theme_test.go).
func Light() *toolkit.Theme {
	t := toolkit.DefaultLight()
	t.Accent = brandDeep
	t.Border = toolkit.RGB(0x6B, 0x72, 0x80)
	return t
}

// Dark is the dark theme: the light end of the gradient as accent, and a
// border light enough for the same 3:1.
func Dark() *toolkit.Theme {
	t := toolkit.DefaultDark()
	t.Accent = brandLight
	t.Background = toolkit.RGB(0x0F, 0x14, 0x19)
	t.Surface = toolkit.RGB(0x16, 0x1C, 0x23)
	t.Border = toolkit.RGB(0x8A, 0x93, 0xA0)
	return t
}

// sidebarGround is the navigation panel's ground: the theme's SurfaceAlt,
// set off from the content by a Border line.
func sidebarGround(t *toolkit.Theme) toolkit.RGBA { return t.SurfaceAlt }

// StateColor is the badge colour of a container state.
func StateColor(state string, exitCode int, exited bool) (toolkit.RGBA, bool) {
	switch state {
	case "running":
		return toolkit.RGB(0x2E, 0x9E, 0x5B), true
	case "exited", "dead":
		if exited && exitCode != 0 {
			return toolkit.RGB(0xC6, 0x3B, 0x3B), true
		}
		return toolkit.RGB(0x7A, 0x82, 0x8C), true
	case "created":
		return brandDeep, true
	case "paused", "restarting", "removing":
		return toolkit.RGB(0xD0, 0x8C, 0x1C), true
	}
	return toolkit.RGBA{}, false
}

// termDefaults are the terminal's own colours (dark in both themes, as a
// console reads best).
var (
	termFG = toolkit.RGB(0xE6, 0xE1, 0xCF)
	termBG = toolkit.RGB(0x0F, 0x14, 0x19)
)

// termTheme is the theme a terminal draws with: its default colours, the
// brand accent for selection.
func termTheme(base *toolkit.Theme) *toolkit.Theme {
	t := *base
	t.OnSurface, t.Surface = termFG, termBG
	return &t
}
