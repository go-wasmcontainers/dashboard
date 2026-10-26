package ui

import (
	"math"
	"testing"

	"github.com/go-widgets/toolkit"
)

// TestContrastRatioKnownValues validates the measure on values the WCAG
// definition fixes: black on white is 21:1, a colour on itself 1:1, and
// #767676 on white the well-known 4.54:1.
func TestContrastRatioKnownValues(t *testing.T) {
	for _, c := range []struct {
		a, b toolkit.RGBA
		want float64
	}{
		{toolkit.RGB(0, 0, 0), toolkit.RGB(255, 255, 255), 21},
		{toolkit.RGB(0x12, 0x34, 0x56), toolkit.RGB(0x12, 0x34, 0x56), 1},
		{toolkit.RGB(0x76, 0x76, 0x76), toolkit.RGB(255, 255, 255), 4.54},
	} {
		if got := toolkit.ContrastRatio(c.a, c.b); math.Abs(got-c.want) > 0.01 {
			t.Errorf("ContrastRatio(%v, %v) = %.3f, want %.2f", c.a, c.b, got, c.want)
		}
	}
}

// TestBorderContrast: the border delimits panels and tables against every
// ground it touches with at least 3:1 (WCAG 2.2 SC 1.4.11), in both themes.
func TestBorderContrast(t *testing.T) {
	for name, th := range map[string]*toolkit.Theme{"light": Light(), "dark": Dark()} {
		for ground, c := range map[string]toolkit.RGBA{
			"background": th.Background, "surface": th.Surface, "surface-alt (header, sidebar)": th.SurfaceAlt,
		} {
			r := toolkit.ContrastRatio(th.Border, c)
			t.Logf("%s border on %s: %.2f:1", name, ground, r)
			if r < 3 {
				t.Errorf("%s border on %s: %.2f:1, want >= 3", name, ground, r)
			}
		}
	}
}
