package vm

import (
	"context"
	"strings"
	"testing"
	"time"

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

func TestRunLive(t *testing.T) {
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

	// With an event stream: each event refreshes; when it ends, it polls.
	h = newHarness(t)
	events := make(chan engine.Event, 1)
	h.f.set(func() { h.f.events = events })
	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() { h.app.RunLive(ctx, time.Millisecond); close(done) }()
	h.until("live", func() bool { return h.app.Live.Get() == "live (GET /events)" })
	n := h.app.Refreshed.Get()
	events <- engine.Event{Type: "container", Action: "start"}
	h.until("event refresh", func() bool { return h.app.Refreshed.Get() > n })
	h.f.set(func() { h.f.events = nil })
	close(events)
	h.until("ended", func() bool { return strings.HasSuffix(h.app.Live.Get(), "(event stream ended)") })
	cancel()
	<-done
	h.settle()

	// Cancelled while following the stream.
	h = newHarness(t)
	h.f.set(func() { h.f.events = make(chan engine.Event) })
	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() { h.app.RunLive(ctx, time.Hour); close(done) }()
	h.until("live", func() bool { return h.app.Live.Get() == "live (GET /events)" })
	cancel()
	<-done
	h.settle()
}
