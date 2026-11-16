package vm

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/go-wasmcontainers/engine/api"

	"github.com/go-wasmcontainers/dashboard/engine"
)

func TestJoinLines(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"a"}, "a"},
		{[]string{"a", "b"}, "2 commands: a; b"},
		{[]string{"a", "b", "c"}, "3 commands: a; b; c"},
		{[]string{"a", "b", "c", "d", "e"}, "5 commands: a; b; c; … +2 more"},
	} {
		if got := JoinLines(c.in); got != c.want {
			t.Errorf("JoinLines(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestContainersMultiSelect(t *testing.T) {
	h := newHarness(t)
	h.f.containers = []api.ContainerSummary{
		cs("c1", "web", "running", "Up", 1_700_000_003),
		cs("c2", "job", "exited", "Exited (0)", 1_700_000_002),
		cs("c3", "db", "running", "Up", 1_700_000_001),
		cs("c4", "keep", "exited", "Exited (0)", 1_700_000_000),
	}
	h.refresh()
	c := h.app.Containers
	if len(c.Targets()) != 0 || len(c.RemoveJobs()) != 0 {
		t.Fatal("nothing selected, yet there are targets")
	}
	// The anchor alone is the target while the set is empty.
	c.Selected.Set("c1")
	if got := c.Targets(); len(got) != 1 || got[0].ID != "c1" {
		t.Errorf("anchor targets = %v", got)
	}
	c.SelectedSet.Set([]string{"c1", "c2", "c3"})
	if len(c.Targets()) != 3 {
		t.Fatalf("targets = %v", c.Targets())
	}
	if got := JobsLine(c.RemoveJobs()); got != "3 commands: wctr rm -f web; wctr rm job; wctr rm -f db" {
		t.Errorf("hover = %q", got)
	}
	// Start acts on the stopped ones, Stop on the running ones, Restart on all.
	if got := JobsLine(c.StartJobs()); got != "wctr start job" {
		t.Errorf("start = %q", got)
	}
	if got := JobsLine(c.StopJobs()); got != "2 commands: wctr stop web; wctr stop db" {
		t.Errorf("stop = %q", got)
	}
	if len(c.RestartJobs()) != 3 {
		t.Errorf("restart = %d jobs", len(c.RestartJobs()))
	}

	// Remove takes the three, never the fourth; one failing does not stop the others.
	h.f.setErr("rm c2", errors.New("busy"))
	c.RemoveSelected()
	h.until("three removed", func() bool {
		return h.f.called("rm -f c1") && h.f.called("rm c2") && h.f.called("rm -f c3") && h.app.Error.Get() != ""
	})
	if h.f.called("rm c4") {
		t.Error("the unselected container was removed")
	}
	if got := h.app.Error.Get(); got != "1 of 3 failed: job: busy" {
		t.Errorf("Error = %q", got)
	}
	if got := h.app.LastCommand.Get(); !strings.HasPrefix(got, "3 commands: wctr rm -f web") {
		t.Errorf("LastCommand = %q", got)
	}
	h.settle()

	// Start, Stop and Restart do the same.
	c.StopSelected()
	h.until("stops", func() bool { return h.f.called("stop c1") && h.f.called("stop c3") })
	c.StartSelected()
	h.until("start", func() bool { return h.f.called("start c2") })
	c.RestartSelected()
	h.until("restarts", func() bool { return h.f.called("restart c1") && h.f.called("restart c2") && h.f.called("restart c3") })
	h.settle()

	// A refresh drops rows that went away from the set.
	h.f.set(func() { h.f.containers = h.f.containers[3:] })
	h.refresh()
	if got := c.SelectedSet.Get(); len(got) != 0 {
		t.Errorf("the set outlived its containers: %v", got)
	}
}

func TestImagesVolumesNetworksMultiSelect(t *testing.T) {
	h := newHarness(t)
	h.f.images = []api.ImageSummary{
		{ID: "sha256:aaaa", RepoTags: []string{"a:1"}},
		{ID: "sha256:bbbb", RepoTags: []string{"b:1"}},
		{ID: "sha256:cccc"},
		{ID: "sha256:dddd", RepoTags: []string{"d:1"}},
	}
	h.f.set(func() {
		h.f.volErr, h.f.netErr = nil, nil
		h.f.volumes = []engine.Volume{{Name: "v1"}, {Name: "v2"}, {Name: "v3"}, {Name: "v4"}}
		h.f.networks = []engine.Network{{Name: "n1"}, {Name: "n2"}, {Name: "n3"}, {Name: "n4"}}
	})
	h.refresh()

	im := h.app.Images
	var keys []string
	for i := 0; i < im.Rows.Len(); i++ {
		if r := im.Rows.At(i); r.Ref != "d:1" {
			keys = append(keys, r.Key())
		}
	}
	im.SelectedSet.Set(keys)
	if got := JobsLine(im.RemoveJobs(true)); !strings.HasPrefix(got, "3 commands: wctr rmi -f ") {
		t.Errorf("images hover = %q", got)
	}
	im.RemoveSelected(true)
	h.until("images", func() bool {
		return h.f.called("rmi -f a:1") && h.f.called("rmi -f b:1") && h.f.called("rmi -f sha256:cccc")
	})
	if h.f.called("rmi -f d:1") {
		t.Error("the unselected image was removed")
	}

	v := h.app.Volumes
	v.SelectedSet.Set([]string{"v1", "v2", "v3"})
	if got := JobsLine(v.RemoveJobs(false)); got != "3 commands: wctr volume rm v1; wctr volume rm v2; wctr volume rm v3" {
		t.Errorf("volumes hover = %q", got)
	}
	v.RemoveSelected(false)
	h.until("volumes", func() bool {
		return h.f.called("volume rm v1") && h.f.called("volume rm v2") && h.f.called("volume rm v3")
	})
	if h.f.called("volume rm v4") {
		t.Error("the unselected volume was removed")
	}

	n := h.app.Networks
	n.SelectedSet.Set([]string{"n1", "n2", "n3"})
	if got := JobsLine(n.RemoveJobs()); got != "3 commands: wctr network rm n1; wctr network rm n2; wctr network rm n3" {
		t.Errorf("networks hover = %q", got)
	}
	h.f.setErr("network rm n2", errors.New("in use"))
	n.RemoveSelected()
	h.until("networks", func() bool {
		return h.f.called("network rm n1") && h.f.called("network rm n3") && h.app.Error.Get() != ""
	})
	if got := h.app.Error.Get(); got != "1 of 3 failed: n2: in use" || h.f.called("network rm n4") {
		t.Errorf("Error = %q, n4 removed = %v", got, h.f.called("network rm n4"))
	}
	h.settle()

	// Rows that vanish leave the sets.
	h.f.set(func() { h.f.volumes, h.f.networks = h.f.volumes[3:], h.f.networks[1:] })
	h.refresh()
	if len(v.SelectedSet.Get()) != 0 || !slices.Equal(n.SelectedSet.Get(), []string{"n2", "n3"}) {
		t.Errorf("sets = %v %v", v.SelectedSet.Get(), n.SelectedSet.Get())
	}
}

func TestDoAllOneJobIsDo(t *testing.T) {
	h := newHarness(t)
	h.app.DoAll(nil) // nothing to do: nothing happens
	if h.app.Pending.Get() != 0 {
		t.Error("an empty action is pending")
	}
	h.app.DoAll([]Job{{Line: "wctr x", Name: "x", Run: func(ctx context.Context) error { return errors.New("no") }}})
	h.until("one job", func() bool { return h.app.Error.Get() == "no" })
	h.settle()
}
