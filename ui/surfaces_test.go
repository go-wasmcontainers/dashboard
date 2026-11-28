package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-widgets/webcanvas"

	"github.com/go-wasmcontainers/dashboard/vm"
)

// frame is a fake ImageBitmap.
type frame struct {
	name   string
	closed *map[string]int
}

func (f frame) Close() { (*f.closed)[f.name]++ }

// backing is a fake page side: one backing image per surface.
type backing struct {
	images    map[uint32]string
	presented []string
	forgotten []uint32
}

func (b *backing) Present(id uint32, f vm.Frame, w, h int) {
	ff := f.(frame)
	b.images[id] = ff.name
	b.presented = append(b.presented, fmt.Sprintf("%s %dx%d", ff.name, w, h))
	f.Close()
}

func (b *backing) Image(id uint32) (webcanvas.Image, bool) {
	n, ok := b.images[id]
	if !ok {
		return webcanvas.Image{}, false
	}
	return webcanvas.NewImage(n), true
}

func (b *backing) Forget(id uint32) {
	delete(b.images, id)
	b.forgotten = append(b.forgotten, id)
}

// The pane's sound controls: the buttons are on a live pane and step the volume
// and mute the container's sound through the view model (the page's audio
// graph is told), the line shows the level, a closed pane has none, and
// the frame area is still inside the pane under them.
func TestSurfacePaneSound(t *testing.T) {
	r := newRig(t)
	r.until("engine", func(p Probe) bool { return strings.HasPrefix(p.Status[0], "engine test") })
	sf := r.app.Surfaces
	type call struct {
		c string
		v float64
		m bool
	}
	var calls []call
	sf.SetAudio(func(c string, v float64, m bool) bool { calls = append(calls, call{c, v, m}); return true })
	sf.Open(1, "cafe", 7, 640, 480)
	r.app.View.Set(vm.ViewSurfaces)
	r.frame()
	p := r.s.Probe()
	for _, k := range []string{"surface.1.voldown", "surface.1.volup", "surface.1.mute"} {
		b, ok := p.Boxes[k]
		if !ok || b.W <= 0 || b.H <= 0 {
			t.Fatalf("%s missing from %v", k, p.Boxes)
		}
	}
	if p.SurfaceSound[0] != "sound 100%" {
		t.Fatalf("sound line %q", p.SurfaceSound[0])
	}
	r.click("surface.1.voldown")
	r.click("surface.1.voldown")
	r.frame()
	if s := r.s.Probe().SurfaceSound[0]; s != "sound 80%" {
		t.Errorf("after two steps down: %q", s)
	}
	r.click("surface.1.volup")
	r.click("surface.1.mute")
	r.frame()
	if s := r.s.Probe().SurfaceSound[0]; s != "sound muted (90%)" {
		t.Errorf("after up and mute: %q", s)
	}
	if last := calls[len(calls)-1]; last != (call{"cafe", 0.9, true}) {
		t.Errorf("the page was told %+v", last)
	}
	r.click("surface.1.mute")
	if last := calls[len(calls)-1]; last != (call{"cafe", 0.9, false}) {
		t.Errorf("unmute: %+v", last)
	}
	// A closed pane has no sound controls (the container is gone).
	sf.Close(1)
	r.frame()
	if _, ok := r.s.Probe().Boxes["surface.1.mute"]; ok {
		t.Error("a closed pane still has a mute button")
	}
}

func TestFit(t *testing.T) {
	for _, c := range []struct {
		r    rect
		w, h int
		want rect
	}{
		{rect{X: 0, Y: 0, W: 200, H: 100}, 640, 480, rect{X: 33, Y: 0, W: 133, H: 100}},
		{rect{X: 10, Y: 10, W: 100, H: 400}, 640, 480, rect{X: 10, Y: 172, W: 100, H: 75}},
		{rect{W: 100, H: 100}, 0, 480, rect{}},
		{rect{W: 0, H: 100}, 640, 480, rect{}},
		{rect{W: 1, H: 1}, 1920, 1, rect{X: 0, Y: 0, W: 1, H: 1}},
	} {
		if got := fit(c.r, c.w, c.h); got != c.want {
			t.Errorf("fit(%+v, %d, %d) = %+v, want %+v", c.r, c.w, c.h, got, c.want)
		}
	}
}

func TestSurfacePanes(t *testing.T) {
	r := newRig(t)
	r.until("engine", func(p Probe) bool { return strings.HasPrefix(p.Status[0], "engine test") })
	sf := r.app.Surfaces
	closed := map[string]int{}
	var acks []string
	sf.SetAck(func(id uint32, seq uint64) { acks = append(acks, fmt.Sprintf("%d:%d", id, seq)) })
	send := func(id uint32, seq uint64, w, h int) {
		sf.Frame(id, seq, w, h, frame{name: fmt.Sprintf("%d:%d", id, seq), closed: &closed})
	}

	// No backing images yet (a page without the feature): frames are closed
	// and acknowledged as they come, and nothing is drawn.
	sf.Open(1, "", 7, 0, 0)
	send(1, 1, 640, 480)
	r.frame()
	if l := r.s.Layers(); l != nil || closed["1:1"] != 1 || len(acks) != 1 {
		t.Fatalf("without a canvas: layers %v, closed %v, acks %v", l, closed, acks)
	}

	b := &backing{images: map[uint32]string{}}
	r.s.SetSurfaceCanvas(b)
	r.s.Modifiers(webcanvas.Modifiers{Alt: true})
	r.s.KeyDown("7")
	r.s.Modifiers(webcanvas.Modifiers{})
	p := r.s.Probe()
	if p.View != "Surfaces" || len(p.Surfaces) != 1 || p.Surfaces[0] != "pid 7 — surface 1 | 640x480, 1 fps" {
		t.Fatalf("one pane: %+v", p.Surfaces)
	}

	// Two surfaces: frames presented into their backing images, drawn as
	// layers fitted to the panes, aspect kept.
	sf.Open(2, "", 8, 320, 240)
	if s := r.s.Probe().Surfaces[1]; !strings.HasSuffix(s, "320x240, waiting for first frame") {
		t.Errorf("second pane: %q", s)
	}
	send(1, 2, 640, 480)
	send(1, 3, 640, 480) // replaces 2, unseen
	send(2, 1, 320, 240)
	r.frame()
	layers := r.s.Layers()
	if len(layers) != 2 || layers[0].Image.Handle() != "1:3" || layers[1].Image.Handle() != "2:1" || layers[0].Alpha != 0 {
		t.Fatalf("layers = %+v (presented %v, images %v, view %d, panes %d)", layers, b.presented, b.images, r.app.View.Get(), len(r.s.panes))
	}
	for i, l := range layers {
		if l.W*480 > l.H*640+640 || l.W*480 < l.H*640-640 {
			t.Errorf("layer %d is %dx%d: aspect not kept", i, l.W, l.H)
		}
		if pane := r.s.panes[i].body; l.X < pane.X || l.Y < pane.Y || l.X+l.W > pane.X+pane.W || l.Y+l.H > pane.Y+pane.H {
			t.Errorf("layer %d %+v outside its pane %+v", i, l, pane)
		}
	}
	if len(b.presented) != 2 || b.presented[0] != "1:3 640x480" {
		t.Errorf("presented = %v", b.presented)
	}

	// In another view frames are still presented and acknowledged, not drawn.
	r.app.View.Set(vm.ViewContainers)
	send(1, 4, 640, 480)
	if l := r.s.Layers(); l != nil || b.images[1] != "1:4" {
		t.Errorf("another view: layers %v, image %q", l, b.images[1])
	}

	// Close: the last frame stays, greyed; Dismiss forgets it.
	r.app.View.Set(vm.ViewSurfaces)
	sf.Close(2)
	r.frame()
	layers = r.s.Layers()
	if len(layers) != 2 || layers[1].Alpha != closedAlpha {
		t.Errorf("closed layer = %+v", layers)
	}
	if s := r.s.Probe().Surfaces[1]; !strings.HasSuffix(s, "closed") {
		t.Errorf("closed pane: %q", s)
	}
	r.click("surface.2.dismiss")
	if len(r.s.Probe().Surfaces) != 1 || len(b.forgotten) != 1 || b.forgotten[0] != 2 {
		t.Errorf("dismiss: %v %v", r.s.Probe().Surfaces, b.forgotten)
	}
	// A surface that goes away without Dismiss (reopened elsewhere, or the
	// page's own removal) is forgotten too.
	sf.Dismiss(1)
	if len(b.forgotten) != 2 {
		t.Errorf("forgotten = %v", b.forgotten)
	}
	if p := r.s.Probe(); len(p.Surfaces) != 0 || p.Boxes["surface.2.dismiss"].W != 0 {
		t.Errorf("empty: %+v", p.Surfaces)
	}
	r.frame()

	// The leak check: every frame closed once, acknowledged once.
	for _, n := range []string{"1:1", "1:2", "1:3", "1:4", "2:1"} {
		k := 0
		for _, a := range acks {
			if a == n {
				k++
			}
		}
		if closed[n] != 1 || k != 1 {
			t.Errorf("frame %s closed %d, acknowledged %d", n, closed[n], k)
		}
	}
}
