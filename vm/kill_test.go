package vm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-wasmcontainers/engine/api"
)

// TestKillRetries: a kill the engine drops (engine v0.3.0, a container
// still starting) is sent again until the container stops.
func TestKillRetries(t *testing.T) {
	old := killRetry
	killRetry = 5 * time.Millisecond
	defer func() { killRetry = old }()
	h := newHarness(t)
	h.f.containers = []api.ContainerSummary{cs("c1", "web", "running", "Up", 1)}
	h.refresh()
	web, _ := h.app.Containers.Find("c1")
	gate := make(chan struct{})
	h.f.set(func() { h.f.stopGate = gate })
	h.app.Containers.Stop(web)
	h.until("a second kill", func() bool { return h.f.count("kill c1") >= 3 })
	close(gate)
	h.until("stopped", func() bool { return h.app.Pending.Get() == 0 })
	h.settle()

	// Cancelled while waiting.
	gate = make(chan struct{})
	h.f.set(func() { h.f.stopGate = gate })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- h.app.Containers.kill(ctx, web) }()
	h.until("killing", func() bool { return h.f.count("kill c1") >= 4 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled kill = %v", err)
	}
	close(gate)
}
