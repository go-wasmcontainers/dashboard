package vm

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-wasmcontainers/engine/api"

	"github.com/go-wasmcontainers/dashboard/engine"
)

func (f *fake) count(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := 0
	for _, c := range f.calls {
		if c == name {
			k++
		}
	}
	return k
}

func TestRunLivePolls(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.app.RunLive(ctx, time.Millisecond); close(done) }()
	h.until("polling", func() bool { return strings.HasPrefix(h.app.Live.Get(), "polling every 1ms (the engine serves no") })
	h.until("polled", func() bool { return h.app.Refreshed.Get() > 3 })
	h.until("tries the stream again", func() bool { return h.f.count("events") >= 2 })
	cancel()
	<-done
	h.settle()
}

// TestRunLiveReconnects: events refresh; an ended stream resynchronises
// and reconnects from the last event's time; a stream that keeps failing
// backs off.
func TestRunLiveReconnects(t *testing.T) {
	oldD, oldM := ReconnectDelay, MaxReconnectDelay
	ReconnectDelay, MaxReconnectDelay = time.Millisecond, 4*time.Millisecond
	defer func() { ReconnectDelay, MaxReconnectDelay = oldD, oldM }()
	h := newHarness(t)
	first := make(chan engine.Event, 1)
	h.f.set(func() { h.f.events = first; h.f.eventsErr = nil })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.app.RunLive(ctx, time.Hour); close(done) }()
	h.until("live", func() bool { return h.app.Live.Get() == "live (GET /events)" })
	n := h.app.Refreshed.Get()
	first <- engine.Event{Type: "container", Action: "start", Time: 100}
	h.until("event refresh", func() bool { return h.app.Refreshed.Get() > n })
	// The engine cuts the stream: reconnect from the last event's time.
	second := make(chan engine.Event)
	h.f.set(func() { h.f.events = second })
	close(first)
	h.until("reconnected", func() bool {
		return len(get(h.f, func() []int64 { return h.f.since })) >= 2
	})
	if s := get(h.f, func() []int64 { return h.f.since }); !slices.Equal(s[:2], []int64{0, 100}) {
		t.Errorf("since = %v", s)
	}
	// It keeps failing: the label says why.
	h.f.set(func() {
		h.f.eventsErr = errors.New("slow reader cut off")
		h.f.events = nil
	})
	close(second)
	h.until("failing", func() bool { return h.app.Live.Get() == "reconnecting to GET /events (slow reader cut off)" })
	h.until("backing off", func() bool { return h.f.count("events") >= 6 })
	cancel()
	<-done
	h.settle()

	// A stream that ends before any event reconnects from now.
	h = newHarness(t)
	empty := make(chan engine.Event)
	close(empty)
	h.f.set(func() { h.f.events = empty; h.f.eventsErr = nil })
	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() { h.app.RunLive(ctx, time.Hour); close(done) }()
	h.until("reconnect from now", func() bool {
		s := get(h.f, func() []int64 { return h.f.since })
		return len(s) >= 2 && s[1] == 1_700_000_100
	})
	h.until("label", func() bool { return h.app.Live.Get() == "reconnecting to GET /events (the stream ended)" })
	cancel()
	<-done
	h.settle()

	// Cancelled while following the stream, and while waiting to reconnect.
	for _, wait := range []bool{false, true} {
		h = newHarness(t)
		ch := make(chan engine.Event)
		if wait {
			close(ch)
			ReconnectDelay = time.Hour
		}
		h.f.set(func() { h.f.events = ch; h.f.eventsErr = nil })
		ctx, cancel = context.WithCancel(context.Background())
		done = make(chan struct{})
		go func() { h.app.RunLive(ctx, time.Hour); close(done) }()
		h.until("connected", func() bool { return h.f.count("events") >= 1 })
		time.Sleep(5 * time.Millisecond)
		cancel()
		<-done
		h.settle()
	}
}

func TestStats(t *testing.T) {
	h := newHarness(t)
	h.f.containers = []api.ContainerSummary{cs("c1", "web", "running", "Up", 1), cs("c2", "job", "exited", "Exited (0)", 2)}
	h.f.inspect = `{"State":{"Status":"running","Running":true},"Config":{}}`
	reading := func(usage, limit uint64) engine.Stats {
		var s engine.Stats
		s.MemoryStats.Usage, s.MemoryStats.Limit = usage, limit
		s.Wctr.CPU = "not measured\x1b"
		s.Wctr.SizeRw, s.Wctr.LogBytes = 2000, 3000
		s.PidsStats.Current = 1
		s.BlkioStats.IOServiceBytesRecursive = []api.BlkioEntry{{Op: "Write", Value: 4000}, {Op: "Read", Value: 9}}
		return s
	}
	h.f.stats = []engine.Stats{reading(16<<20, 64<<20)}
	h.refresh()
	d := h.app.Detail
	d.Open("c1")
	h.until("stats", func() bool { return d.Memory.Get() == 0.25 })
	if d.MemoryText.Get() != "16.8MB of 67.1MB" || d.CPU.Get() != "--" || d.CPUNote.Get() != "not measured\uFFFD" ||
		d.Counters.Get() != "written 4kB · layer 2kB · logs 3kB · pids 1" || d.StatsLine.Get() != "wctr stats web" ||
		d.MemHistory.Len() != 1 || !h.f.called("stats c1 stream=true") {
		t.Errorf("stats: %q %q %q %q %q %d", d.MemoryText.Get(), d.CPU.Get(), d.CPUNote.Get(), d.Counters.Get(), d.StatsLine.Get(), d.MemHistory.Len())
	}
	// History is bounded; no limit means no gauge; measured CPU is shown.
	for i := 0; i < MaxHistory+5; i++ {
		d.applyStats(reading(uint64(i), 0))
	}
	if d.MemHistory.Len() != MaxHistory || d.Memory.Get() != -1 || d.MemoryText.Get() != Size(MaxHistory+4) {
		t.Errorf("history %d memory %v %q", d.MemHistory.Len(), d.Memory.Get(), d.MemoryText.Get())
	}
	s := reading(1, 2)
	s.CPUStats.CPUUsage.TotalUsage, s.CPUStats.SystemCPUUsage = 1, 4
	d.applyStats(s)
	if d.CPU.Get() != "25.0%" {
		t.Errorf("cpu = %q", d.CPU.Get())
	}
	// A stopped container: one reading; an error shows.
	h.f.set(func() {
		h.f.inspect = `{"State":{"Status":"exited","Running":false,"FinishedAt":"x"},"Config":{}}`
		h.f.statsErr = errors.New("no stats")
	})
	d.Open("c2")
	h.until("one reading", func() bool { return h.f.called("stats c2 stream=false") && d.Counters.Get() == "stats: no stats" })
	if d.StatsLine.Get() != "wctr stats --no-stream job" {
		t.Errorf("stats line = %q", d.StatsLine.Get())
	}
	d.Close()
	if d.Memory.Get() != -1 || d.MemHistory.Len() != 0 || d.StatsLine.Get() != "" {
		t.Error("Close kept the stats")
	}
	h.settle()
}

func TestRunFormVolumes(t *testing.T) {
	h := newHarness(t)
	f := h.app.Images.Run
	f.Reset("hello")
	f.Volumes.Set("data:/data, cache:/c:ro\n , ")
	_, req, line, err := f.Request()
	if err != nil || line != "wctr run -d -v data:/data -v cache:/c:ro hello" || !slices.Equal(req.HostConfig.Binds, []string{"data:/data", "cache:/c:ro"}) {
		t.Errorf("Request = %q %v %v", line, req.HostConfig.Binds, err)
	}
	for _, bad := range []string{"data", "/host:/data", "data:rel", "data:/d:xx", ":/d", "a:/b:ro:x"} {
		f.Volumes.Set(bad)
		if !strings.HasPrefix(f.Error.Get(), "volume ") {
			t.Errorf("%q: error %q", bad, f.Error.Get())
		}
	}
}
