package vm

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/go-wasmcontainers/engine/container"
	"github.com/go-wasmcontainers/engine/rootfs"
	"github.com/go-wasmcontainers/engine/runtime"
)

// The store is shared: a level set on a container with no pane shows on the
// pane that opens later, and a level set through a pane shows in the store.
func TestSoundSharedWithSurfaces(t *testing.T) {
	h := newHarness(t)
	snd, sf := h.app.Sound, h.app.Surfaces
	snd.SetVolume("aaaa", 30)
	snd.ToggleMute("aaaa")
	sf.Open(1, "aaaa", 1, 64, 48)
	if r, _ := sf.Find(1); r.Volume != 30 || !r.Muted {
		t.Fatalf("the pane shows %+v, want volume 30 muted", r)
	}
	sf.StepVolume(1, VolumeStep)
	if v, m := snd.Level("aaaa"); v != 40 || !m {
		t.Errorf("store = %d %v", v, m)
	}
	// A change made in the store repaints the pane's row.
	snd.ToggleMute("aaaa")
	if r, _ := sf.Find(1); r.Muted {
		t.Errorf("the pane did not follow the store: %+v", r)
	}
}

func TestSoundGroupAndGlobalMute(t *testing.T) {
	s := newSound()
	type call struct {
		c string
		v float64
		m bool
	}
	var calls []call
	var globals []bool
	s.SetAudio(func(c string, v float64, m bool) bool { calls = append(calls, call{c, v, m}); return true })
	s.SetGlobalAudio(func(m bool) { globals = append(globals, m) })
	revs := 0
	s.Revision.SubscribeChanged(func() { revs++ })

	s.SetVolume("a", 50)
	s.SetMuted([]string{"a", "b"}, true)
	if !s.AllMuted([]string{"a", "b"}) || s.AllMuted([]string{"a", "b", "c"}) || s.AllMuted(nil) {
		t.Errorf("AllMuted wrong")
	}
	if v, _ := s.Level("a"); v != 50 {
		t.Errorf("a group mute changed the volume: %d", v)
	}
	if last := calls[len(calls)-1]; last != (call{"b", 1, true}) {
		t.Errorf("last call %+v", last)
	}
	before := revs
	s.SetMuted([]string{"a", "b"}, false)
	if revs != before+1 {
		t.Errorf("a group change published %d times, want once", revs-before)
	}
	s.SetAllMuted(true)
	if !s.All.Get() || !reflect.DeepEqual(globals, []bool{true}) {
		t.Errorf("global = %v %v", s.All.Get(), globals)
	}
	if _, m := s.Level("a"); m {
		t.Errorf("the page-wide mute must not rewrite the per-container levels")
	}
}

func TestSoundBounds(t *testing.T) {
	s := newSound()
	s.SetVolume("a", 250)
	if v, _ := s.Level("a"); v != 100 {
		t.Errorf("above: %d", v)
	}
	s.SetVolume("a", -3)
	if v, _ := s.Level("a"); v != 0 {
		t.Errorf("below: %d", v)
	}
	s.SetVolume("", 10) // no container: nothing
	if len(s.levels) != 1 {
		t.Errorf("an empty key was stored")
	}
	for i := 0; i < MaxSoundKeys+50; i++ {
		s.SetVolume(Clean(string(rune('a'+i%26))+itoa(i), 64), 10)
	}
	if len(s.levels) > MaxSoundKeys {
		t.Errorf("%d levels kept, bound %d", len(s.levels), MaxSoundKeys)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}

func TestSoundActive(t *testing.T) {
	s := newSound()
	revs := 0
	s.Revision.SubscribeChanged(func() { revs++ })
	s.SetActive([]string{"b", "a", "b"})
	if !s.Active("a") || s.Active("z") || !reflect.DeepEqual(s.ActiveIDs(), []string{"a", "b"}) {
		t.Errorf("active = %v", s.ActiveIDs())
	}
	s.SetActive([]string{"a", "b"}) // same set: no change published
	if revs != 1 {
		t.Errorf("published %d times, want 1", revs)
	}
}

// idProbe is an executor that records the runtime.Spec.ID the engine gives a
// started container: the key its sound is played under.
type idProbe struct{ got chan string }

type doneProc struct{}

func (doneProc) Wait() runtime.Result { return runtime.Result{} }
func (doneProc) Kill()                {}

func (p idProbe) Start(_ context.Context, spec runtime.Spec) (runtime.Process, error) {
	p.got <- spec.ID
	return doneProc{}, nil
}
func (idProbe) Close(context.Context) error { return nil }

// The audio key is the container's full ID, taken from the REAL engine: a
// container created and started by the engine's manager is run under
// runtime.Spec.ID (container.go:687, from c.id), the ID its snapshot (the
// inspect and list documents) carries (container.go:263). A mute keyed by that
// ID reaches the page's gain node of the same key.
func TestAudioKeyIsTheContainerID(t *testing.T) {
	probe := idProbe{got: make(chan string, 1)}
	m := container.NewManager(container.Options{Executor: probe})
	img := rootfs.New()
	img.MkdirAll("/usr/bin", 0o755)
	img.WriteFile("/usr/bin/p.wasm", []byte("\x00asm\x01\x00\x00\x00"), 0o755)
	img.Freeze()
	c, err := m.Create(container.CreateOptions{Name: "snd", Lower: img,
		Config: container.Config{Image: "p", ImageID: "sha256:x", Entrypoint: []string{"p"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background(), c.ID()); err != nil {
		t.Fatal(err)
	}
	specID := <-probe.got
	full := c.Snapshot().ID
	if len(full) != 64 || specID != full {
		t.Fatalf("engine plays under %q, its API reports %q", specID, full)
	}
	h := newHarness(t)
	var key string
	h.app.Sound.SetAudio(func(c string, _ float64, _ bool) bool { key = c; return true })
	h.app.Sound.ToggleMute(full)
	if key != specID {
		t.Errorf("audio key %q, the engine's spec ID is %q", key, specID)
	}
}

// At the bound an unmuted level is evicted for a new one; a muted one never is.
func TestSoundBoundEvictsUnmutedNeverMuted(t *testing.T) {
	s := newSound()
	type call struct {
		c string
		v float64
		m bool
	}
	var calls []call
	s.SetAudio(func(c string, v float64, m bool) bool { calls = append(calls, call{c, v, m}); return true })
	s.ToggleMute("k000") // muted, and the smallest key
	for i := 1; i < MaxSoundKeys; i++ {
		v := 50
		if i == 1 || i == 2 {
			v = 100 // at the default level: the only ones that may be evicted
		}
		s.SetVolume(fmt.Sprintf("k%03d", i), v)
	}
	s.SetVolume("fresh", 70)
	if len(s.levels) != MaxSoundKeys {
		t.Fatalf("%d levels", len(s.levels))
	}
	if _, m := s.Level("k000"); !m {
		t.Fatal("the bound unmuted a muted container")
	}
	if _, ok := s.levels["k001"]; ok {
		t.Error("the least unmuted level was not the one evicted")
	}
	if last := calls[len(calls)-2]; last != (call{"k001", 1, false}) {
		t.Errorf("the page was not told to free the evicted key: %+v", last)
	}
	if v, _ := s.Level("fresh"); v != 70 || s.Refused.Get() != "" {
		t.Errorf("fresh %d refused %q", v, s.Refused.Get())
	}
}

// With every kept level muted there is nothing to evict: the new mute is
// refused where the user can read it, and nothing pretends it took.
func TestSoundBoundAllMutedRefusesVisibly(t *testing.T) {
	s := newSound()
	for i := 0; i < MaxSoundKeys; i++ {
		s.ToggleMute(fmt.Sprintf("k%03d", i))
	}
	s.ToggleMute("extra")
	if _, m := s.Level("extra"); m {
		t.Error("a mute past the bound looks applied")
	}
	if !strings.Contains(s.Refused.Get(), "reset one first") {
		t.Errorf("refusal not shown: %q", s.Refused.Get())
	}
	s.ToggleMute("k000") // an unmute frees a slot
	s.ToggleMute("extra")
	if _, m := s.Level("extra"); !m || s.Refused.Get() != "" {
		t.Errorf("extra muted %v refused %q", m, s.Refused.Get())
	}
}

// The page's refusal (its own bound) propagates: no level is kept and the
// refusal is shown, so "muted" is never displayed while the sound plays.
func TestSoundPageRefusalPropagates(t *testing.T) {
	s := newSound()
	s.SetAudio(func(string, float64, bool) bool { return false })
	s.ToggleMute("a")
	if _, m := s.Level("a"); m {
		t.Error("a mute the page refused is shown as muted")
	}
	if !strings.Contains(s.Refused.Get(), "page refused") {
		t.Errorf("refusal = %q", s.Refused.Get())
	}
}

// Evicting never changes a user's setting: a container set to volume 0 keeps it
// when a new key arrives and the only candidates have a non-default level.
func TestSoundEvictNeverChangesASetting(t *testing.T) {
	s := newSound()
	s.SetVolume("k000", 0)
	for i := 1; i < MaxSoundKeys; i++ {
		s.SetVolume(fmt.Sprintf("k%03d", i), 50)
	}
	s.SetVolume("fresh", 70)
	if v, _ := s.Level("k000"); v != 0 {
		t.Errorf("volume 0 became %d", v)
	}
	if _, ok := s.levels["fresh"]; ok {
		t.Error("a level was kept past the bound")
	}
	if !strings.Contains(s.Refused.Get(), "reset one first") {
		t.Errorf("refusal not shown: %q", s.Refused.Get())
	}
}

// A refusal inside a group mute is not erased by a later member that succeeds,
// and it names the members refused.
func TestSoundSetMutedKeepsRefusal(t *testing.T) {
	s := newSound()
	s.SetAudio(func(c string, _ float64, _ bool) bool { return c != "a" })
	s.SetMuted([]string{"a", "b"}, true)
	if _, m := s.Level("a"); m {
		t.Error("a refused member shows muted")
	}
	if got := s.Refused.Get(); !strings.Contains(got, "not applied to 1: a") {
		t.Errorf("refusal = %q", got)
	}
	s.SetMuted([]string{"", "b"}, true) // an empty name is skipped, not refused
	if s.Refused.Get() != "" {
		t.Errorf("a clean call kept a stale refusal: %q", s.Refused.Get())
	}
}

// A refused change publishes nothing and reports false; without a page
// attached a level simply takes.
func TestSoundRefusalPublishesNothing(t *testing.T) {
	s := newSound()
	s.SetAudio(func(string, float64, bool) bool { return false })
	rev := s.Revision.Get()
	if s.update("a", func(l level) level { l.muted = true; return l }, true) {
		t.Error("a refused change reports success")
	}
	if s.Revision.Get() != rev {
		t.Error("a refused change was published")
	}
	s.SetAudio(nil)
	s.ToggleMute("b")
	if _, m := s.Level("b"); !m || s.Refused.Get() != "" {
		t.Errorf("no page attached: muted %v refused %q", m, s.Refused.Get())
	}
}
