package ui

import (
	"bytes"
	"runtime"
	"testing"
	"time"

	"github.com/go-widgets/toolkit"
)

// fillBackground sets every pixel, whatever the buffer's size (the doubling
// copies end on a partial block) and leaves nothing of what was there.
func TestFillBackground(t *testing.T) {
	c := toolkit.RGBA{R: 0x0F, G: 0x14, B: 0x19, A: 0xFF}
	for _, px := range []int{0, 1, 2, 3, 5, 8, 9, 100, 1023, 1024, 1025, 640 * 480} {
		buf := bytes.Repeat([]byte{0xAA}, 4*px)
		fillBackground(buf, c)
		for i := 0; i < len(buf); i += 4 {
			if buf[i] != c.R || buf[i+1] != c.G || buf[i+2] != c.B || buf[i+3] != c.A {
				t.Fatalf("%d pixels: pixel %d is %v, want %v", px, i/4, buf[i:i+4], c)
			}
		}
	}
	fillBackground(make([]byte, 3), c) // a buffer under a pixel: untouched, no panic
}

// The dashboard repaints its whole scene for every frame a guest presents, so
// what a repaint costs is what the guest waits for in present. The background
// fill alone was 4.4 ms of Chrome's wasm at 1280x800 (CPU profile of the pane
// lane, go-wasmcontainers/playground#63), 26 % of a 60 frames-a-second budget of
// 16.7 ms. The bound is derived from that budget, not from this machine: the
// fill of a 1280x800 buffer must take under 1/20 of it (0.83 ms), measured as the
// best of several runs. It is checked where the page runs, under GOOS=js (the old
// pixel loop takes 3 to 5 ms there); natively the old loop was within the bound
// too, so a native run only checks the answer.
func TestFillBackgroundCostIsAFractionOfTheFrameBudget(t *testing.T) {
	buf := make([]byte, 4*1280*800)
	c := toolkit.RGBA{R: 0x0F, G: 0x14, B: 0x19, A: 0xFF}
	best := time.Hour
	for i := 0; i < 15; i++ {
		t0 := time.Now()
		fillBackground(buf, c)
		best = min(best, time.Since(t0))
	}
	budget := time.Second / 60
	t.Logf("fill of 1280x800 on %s: best %v (budget %v, bound %v)", runtime.GOOS, best, budget, budget/20)
	if runtime.GOOS != "js" {
		return
	}
	if best > budget/20 {
		t.Errorf("filling the 1280x800 background takes %v, over 1/20 of the frame budget %v", best, budget)
	}
}
