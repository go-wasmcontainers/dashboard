package vm

import (
	"fmt"
	"sort"

	"github.com/go-widgets/mvvm"
)

// level is one container's sound level.
type level struct {
	volume int
	muted  bool
}

// Sound is the one store of sound levels (volume 0 to 100, muted), keyed by
// the audio key: the stream's container, which the engine takes from
// runtime.Spec.ID, that is the container's full ID. A GPU pane and a node of
// the topology graph hold the same *Sound, so each shows what the other did,
// and a container with no pane can still be muted.
//
// Three levels act on the page's audio graph (the playground's web/audio.js):
// a container's gain node (SetAudio), the page-wide mute (SetGlobalAudio) and,
// for a group (a network, a project), one call per member (SetMuted). Per
// container levels survive the page-wide mute, as in the page.
type Sound struct {
	audio  func(container string, volume float64, muted bool) bool
	global func(muted bool)
	levels map[string]level
	active map[string]bool

	// All is the page-wide mute.
	All *mvvm.Observable[bool]
	// Refused says why the last level change did not take ("" when it did).
	Refused *mvvm.Observable[string]
	// Revision counts changes (a level, the page mute, the active set): views
	// that draw sound state subscribe to it.
	Revision *mvvm.Observable[int]
}

func newSound() *Sound {
	return &Sound{levels: map[string]level{}, active: map[string]bool{},
		All: mvvm.NewObservable(false), Refused: mvvm.NewObservable(""), Revision: mvvm.NewObservable(0)}
}

// MaxSoundKeys bounds the levels kept (one per distinct container named). It is
// the page's own bound (web/audio.js keeps 256 preferences), so the two agree.
const MaxSoundKeys = 256

// SetAudio sets how a container's level reaches the page's audio graph. f
// reports whether the page took the level; false is a refusal the dashboard
// shows (Refused) instead of a mute that sounds on.
func (s *Sound) SetAudio(f func(container string, volume float64, muted bool) bool) { s.audio = f }

// SetGlobalAudio sets how the page-wide mute reaches the page's audio graph.
func (s *Sound) SetGlobalAudio(f func(muted bool)) { s.global = f }

func (s *Sound) bump() { s.Revision.Set(s.Revision.Get() + 1) }

// Level is the volume (0 to 100) and mute of a container; unset is 100,
// not muted.
func (s *Sound) Level(container string) (volume int, muted bool) {
	if l, ok := s.levels[container]; ok {
		return l.volume, l.muted
	}
	return 100, false
}

// evict frees one slot by dropping an unmuted level (the least key, so the
// choice is the same every time), returning the page's audio to its defaults
// for it. A muted level is never dropped: that would unmute it. It reports
// whether a slot was freed.
func (s *Sound) evict() bool {
	victim := ""
	for k, l := range s.levels {
		if !l.muted && (victim == "" || k < victim) {
			victim = k
		}
	}
	if victim == "" {
		return false
	}
	delete(s.levels, victim)
	if s.audio != nil {
		s.audio(victim, 1, false)
	}
	return true
}

// update applies f to a container's level and reports whether it took. A
// refusal (every kept level is muted and the bound is reached, or the page
// refused the level) leaves the level as it was and says so on Refused: a mute
// that did nothing must never look like a mute.
func (s *Sound) update(container string, f func(level) level, notify bool) bool {
	if container == "" {
		return false
	}
	v, m := s.Level(container)
	_, known := s.levels[container]
	if !known && len(s.levels) >= MaxSoundKeys && !s.evict() {
		s.Refused.Set(fmt.Sprintf("sound: %d containers already have a muted or set level; unmute one first", MaxSoundKeys))
		return false
	}
	l := f(level{v, m})
	l.volume = min(max(l.volume, 0), 100)
	if s.audio != nil && !s.audio(container, float64(l.volume)/100, l.muted) {
		s.Refused.Set("sound: the page refused the level of " + Clean(container, 12))
		return false
	}
	s.levels[container] = l
	s.Refused.Set("")
	if notify {
		s.bump()
	}
	return true
}

// SetVolume sets a container's volume (0 to 100).
func (s *Sound) SetVolume(container string, percent int) {
	s.update(container, func(l level) level { l.volume = percent; return l }, true)
}

// StepVolume changes a container's volume by delta (VolumeStep, or its negative).
func (s *Sound) StepVolume(container string, delta int) {
	s.update(container, func(l level) level { l.volume += delta; return l }, true)
}

// ToggleMute mutes, or unmutes, a container.
func (s *Sound) ToggleMute(container string) {
	s.update(container, func(l level) level { l.muted = !l.muted; return l }, true)
}

// SetMuted mutes or unmutes every container of a group (a network's members,
// a project's services), each by its own level; one change is published.
func (s *Sound) SetMuted(containers []string, muted bool) {
	for _, c := range containers {
		s.update(c, func(l level) level { l.muted = muted; return l }, false)
	}
	s.bump()
}

// AllMuted reports whether every one of the containers is muted (false for none).
func (s *Sound) AllMuted(containers []string) bool {
	if len(containers) == 0 {
		return false
	}
	for _, c := range containers {
		if _, m := s.Level(c); !m {
			return false
		}
	}
	return true
}

// SetAllMuted sets the page-wide mute. Per-container levels are kept.
func (s *Sound) SetAllMuted(muted bool) {
	s.All.Set(muted)
	if s.global != nil {
		s.global(muted)
	}
	s.bump()
}

// SetActive says which containers have a sound stream now (the page reports
// the labels of its open streams). The graph shows a speaker for these only.
func (s *Sound) SetActive(containers []string) {
	next := make(map[string]bool, len(containers))
	for _, c := range containers {
		if len(next) < MaxSoundKeys {
			next[Clean(c, 64)] = true
		}
	}
	if len(next) == len(s.active) {
		same := true
		for c := range next {
			if !s.active[c] {
				same = false
				break
			}
		}
		if same {
			return
		}
	}
	s.active = next
	s.bump()
}

// Active reports whether a container has a sound stream now.
func (s *Sound) Active(container string) bool { return s.active[container] }

// ActiveIDs are the containers with a stream, sorted.
func (s *Sound) ActiveIDs() []string {
	out := make([]string, 0, len(s.active))
	for c := range s.active {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
