package vm

import (
	"reflect"
	"testing"
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
	s.SetAudio(func(c string, v float64, m bool) { calls = append(calls, call{c, v, m}) })
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

// The audio key is the container's full ID: the engine plays a container's
// sound under runtime.Spec.ID, which container.go sets from c.id, and that is
// the ID the API returns on create and lists. The topology keys its speakers
// by the same ID, so a node's mute reaches the page's gain node.
func TestAudioKeyIsTheContainerID(t *testing.T) {
	h := newHarness(t)
	full := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	var key string
	h.app.Sound.SetAudio(func(c string, _ float64, _ bool) { key = c })
	h.app.Sound.ToggleMute(full)
	if key != full {
		t.Errorf("audio key %q, want the full container ID", key)
	}
}
