package vm

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-wasmcontainers/engine/api"
)

// fakeFrame counts its closes.
type fakeFrame struct {
	name   string
	closes *map[string]int
}

func (f fakeFrame) Close() { (*f.closes)[f.name]++ }

// emitter stands in for the page: it hands the Surfaces the messages the
// engine worker posts, and records the acknowledgements.
type emitter struct {
	t      *testing.T
	s      *Surfaces
	closes map[string]int
	made   []string
	acks   []string
}

func newEmitter(t *testing.T, s *Surfaces) *emitter {
	e := &emitter{t: t, s: s, closes: map[string]int{}}
	s.SetAck(func(id uint32, seq uint64) { e.acks = append(e.acks, fmt.Sprintf("%d:%d", id, seq)) })
	return e
}

func (e *emitter) frame(id uint32, seq uint64, w, h int) fakeFrame {
	f := fakeFrame{name: fmt.Sprintf("%d:%d", id, seq), closes: &e.closes}
	e.made = append(e.made, f.name)
	e.s.Frame(id, seq, w, h, f)
	return f
}

// present draws what is waiting, as the view does each frame.
func (e *emitter) present() {
	for _, id := range e.s.IDs() {
		if f, seq, w, h, ok := e.s.Take(id); ok {
			f.Close()
			e.s.Presented(id, seq, w, h)
		}
	}
}

// leaks checks every frame made was closed and acknowledged exactly once.
func (e *emitter) leaks() {
	e.t.Helper()
	acked, made := map[string]int{}, map[string]int{}
	for _, a := range e.acks {
		acked[a]++
	}
	for _, n := range e.made {
		made[n]++
	}
	for n, k := range made {
		if e.closes[n] != k || acked[n] != k {
			e.t.Errorf("frame %s, made %d times: closed %d times, acknowledged %d times", n, k, e.closes[n], acked[n])
		}
	}
	if len(acked) != len(made) {
		e.t.Errorf("%d frames acknowledged, %d made", len(acked), len(made))
	}
}

// A pane's sound controls: volume in steps, mute, kept by container (two panes
// of one container agree, a pane that comes back keeps the level), applied to
// the page's audio graph, and bounded; a pane with no container has no sound.
func TestSurfaceAudioControls(t *testing.T) {
	h := newHarness(t)
	s := h.app.Surfaces
	type call struct {
		c string
		v float64
		m bool
	}
	var calls []call
	s.SetAudio(func(c string, v float64, m bool) { calls = append(calls, call{c, v, m}) })
	s.Open(1, "aaaa", 1, 64, 48)
	s.Open(2, "aaaa", 1, 64, 48)
	s.Open(3, "bbbb", 2, 64, 48)
	s.Open(4, "", 3, 64, 48)
	for _, r := range s.Rows.Slice() {
		if r.Volume != 100 || r.Muted {
			t.Fatalf("a new pane starts at full volume: %+v", r)
		}
	}
	s.StepVolume(1, -VolumeStep)
	s.StepVolume(1, -VolumeStep)
	if r, _ := s.Find(2); r.Volume != 80 {
		t.Errorf("the other pane of the container shows %d, want 80", r.Volume)
	}
	if r, _ := s.Find(3); r.Volume != 100 {
		t.Errorf("another container changed: %d", r.Volume)
	}
	s.ToggleMute(2)
	if r, _ := s.Find(1); !r.Muted || r.Volume != 80 {
		t.Errorf("mute: %+v", r)
	}
	if last := calls[len(calls)-1]; last != (call{"aaaa", 0.8, true}) {
		t.Errorf("the page was told %+v", last)
	}
	s.ToggleMute(1)
	if last := calls[len(calls)-1]; last != (call{"aaaa", 0.8, false}) {
		t.Errorf("unmute: %+v", last)
	}
	// Bounds.
	s.SetVolume(1, 250)
	s.StepVolume(1, VolumeStep)
	if r, _ := s.Find(1); r.Volume != 100 {
		t.Errorf("above the bound: %d", r.Volume)
	}
	s.SetVolume(1, -5)
	if r, _ := s.Find(1); r.Volume != 0 {
		t.Errorf("below the bound: %d", r.Volume)
	}
	// No container, or no pane: nothing is applied.
	n := len(calls)
	s.SetVolume(4, 10)
	s.ToggleMute(4)
	s.SetVolume(99, 10)
	if len(calls) != n {
		t.Errorf("a pane without sound called the page: %v", calls[n:])
	}
	if r, _ := s.Find(4); r.Volume != 100 {
		t.Errorf("pane without container: %+v", r)
	}
	// The level outlives the pane.
	s.SetVolume(3, 30)
	s.Dismiss(3)
	s.Open(3, "bbbb", 2, 64, 48)
	if r, _ := s.Find(3); r.Volume != 30 {
		t.Errorf("a reopened pane lost its level: %d", r.Volume)
	}
}

func TestSurfaces(t *testing.T) {
	h := newHarness(t)
	now := time.Unix(1_700_000_100, 0)
	h.app.SetClock(func() time.Time { return now })
	h.f.set(func() {
		h.f.containers = []api.ContainerSummary{{ID: "c0ffee000000aaaa", Names: []string{"/cube"}, State: "running"}}
	})
	h.refresh()
	s := h.app.Surfaces
	e := newEmitter(t, s)

	// A frame for a surface nobody opened is dropped at once.
	e.frame(9, 1, 10, 10)
	if s.Rows.Len() != 0 || len(e.acks) != 1 {
		t.Fatalf("unknown surface: %d rows, acks %v", s.Rows.Len(), e.acks)
	}

	// Open: default size, the container's name, waiting for the first frame.
	s.Open(1, "c0ffee000000aaaa", 42, 0, 0)
	s.Open(2, "", 7, 4000, 3000)
	s.Open(3, "deadbeef00001111", 8, 320, 200)
	r := s.Rows.Slice()
	if len(r) != 3 || r[0].Title != "cube — surface 1" || r[0].Status != "640x480, waiting for first frame" ||
		r[1].Title != "pid 7 — surface 2" || r[1].W != MaxSurfaceW || r[1].H != MaxSurfaceH ||
		r[2].Title != "deadbeef0000 — surface 3" {
		t.Fatalf("rows = %+v", r)
	}
	if !s.Has(2) || s.Has(5) {
		t.Error("Has")
	}
	if r, ok := s.Find(2); !ok || r.ID != 2 {
		t.Errorf("Find(2) = %+v %v", r, ok)
	}
	if _, ok := s.Find(5); ok {
		t.Error("Find invented a surface")
	}
	s.Resize(3, 800, 600)
	s.Resize(99, 1, 1) // unknown: nothing
	if s.Rows.At(2).W != 800 {
		t.Errorf("resize: %+v", s.Rows.At(2))
	}

	// Frames: only the newest waits; older and out-of-order ones are dropped.
	e.frame(1, 1, 640, 480)
	e.frame(1, 2, 640, 480)
	e.frame(1, 2, 640, 480) // not after the last
	e.frame(1, 1, 640, 480) // older
	if e.closes["1:1"] != 2 || e.closes["1:2"] != 1 {
		t.Errorf("closes = %v", e.closes)
	}
	e.present()
	if r := s.Rows.At(0); !r.Shown || r.Status != "640x480, 1 fps" {
		t.Errorf("after one frame: %+v", r)
	}
	if _, _, _, _, ok := s.Take(1); ok {
		t.Error("a frame was handed over twice")
	}
	if _, _, _, _, ok := s.Take(42); ok {
		t.Error("a frame for no surface")
	}
	// The frame rate counts the last second.
	for i := uint64(3); i < 33; i++ {
		now = now.Add(10 * time.Millisecond)
		e.frame(1, i, 640, 480)
		e.present()
	}
	if r := s.Rows.At(0); r.Status != "640x480, 31 fps" {
		t.Errorf("rate: %+v", r)
	}
	now = now.Add(2 * time.Second)
	h.refresh() // a refresh re-reads the rate: no frames, 0 fps
	if r := s.Rows.At(0); r.Status != "640x480, 0 fps" || s.FPS(77) != 0 {
		t.Errorf("idle rate: %+v", r)
	}
	// The rate window is bounded.
	for i := uint64(33); i < 33+maxTimes+10; i++ {
		e.frame(1, i, 640, 480)
		e.present()
	}
	if s.FPS(1) != maxTimes {
		t.Errorf("bounded rate window = %d", s.FPS(1))
	}

	// Close: a waiting frame is dropped; the pane stays, greyed; later frames
	// are dropped.
	e.frame(2, 1, 1920, 1080)
	s.Close(2)
	s.Close(77)
	e.frame(2, 2, 1920, 1080)
	s.Resize(2, 10, 10)
	if r := s.Rows.At(1); !r.Closed || r.Status != "1920x1080, closed" || r.W != 1920 {
		t.Errorf("closed: %+v", r)
	}
	// Reopening the same ID starts over.
	e.frame(3, 5, 800, 600)
	s.Open(3, "", 9, 100, 100)
	e.frame(3, 1, 100, 100)
	e.present()
	if r := s.Rows.At(2); r.Status != "100x100, 1 fps" {
		t.Errorf("reopened: %+v", r)
	}
	// Dismiss drops a waiting frame and the pane.
	e.frame(3, 2, 100, 100)
	if !s.Dismiss(3) || s.Dismiss(3) || s.Rows.Len() != 2 {
		t.Errorf("dismiss: %d rows", s.Rows.Len())
	}
	s.Presented(3, 9, 1, 1) // late: acknowledged, nothing else
	e.acks = e.acks[:len(e.acks)-1]

	// The pane limit.
	for id := uint32(100); id < 100+MaxSurfaces; id++ {
		s.Open(id, "", 1, 0, 0)
	}
	if s.Rows.Len() != MaxSurfaces || !strings.Contains(h.app.Error.Get(), "at most 16") {
		t.Errorf("limit: %d %q", s.Rows.Len(), h.app.Error.Get())
	}
	// Without an acknowledger nothing breaks.
	s.SetAck(nil)
	s.Frame(500, 1, 1, 1, fakeFrame{name: "x", closes: &e.closes})
	if e.closes["x"] != 1 {
		t.Error("an unacknowledged drop leaked")
	}
	s.SetAck(func(id uint32, seq uint64) { e.acks = append(e.acks, fmt.Sprintf("%d:%d", id, seq)) })
	e.leaks()
	h.settle()
}
