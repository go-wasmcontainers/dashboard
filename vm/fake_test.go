package vm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-wasmcontainers/engine/api"
	"github.com/go-wasmcontainers/engine/cli"
	"github.com/go-widgets/mvvm"

	"github.com/go-wasmcontainers/dashboard/engine"
)

// fake is an in-memory Engine. Every call is recorded.
type fake struct {
	mu         sync.Mutex
	calls      []string
	containers []api.ContainerSummary
	images     []api.ImageSummary
	volumes    []engine.Volume
	volErr     error
	df         *engine.DiskUsage
	inspect    string
	logs       string
	logsErr    error
	err        map[string]error // by call name
	events     chan engine.Event
	eventsErr  error
	pullMsgs   []api.JSONMessage
	noImage    bool          // Create answers "no such image" until a Pull
	gate       chan struct{} // Version waits on it when set
	since      []int64       // the since of each Events call
	stats      []engine.Stats
	statsErr   error
	volInspect string
	prune      api.VolumePruneResponse
	networks   []engine.Network
	netErr     error
	netInspect string
}

func (f *fake) Networks(context.Context) ([]engine.Network, error) {
	f.record("networks")
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.networks, f.netErr
}

func (f *fake) NetworkCreate(_ context.Context, o api.NetworkCreateRequest) (api.NetworkCreateResponse, error) {
	return api.NetworkCreateResponse{ID: "n1"}, f.record("network create " + o.Name)
}

func (f *fake) NetworkInspect(_ context.Context, name string) (json.RawMessage, error) {
	err := f.record("network inspect " + name)
	return json.RawMessage(get(f, func() string { return f.netInspect })), err
}

func (f *fake) NetworkRemove(_ context.Context, name string) error {
	return f.record("network rm " + name)
}

func newFake() *fake {
	return &fake{err: map[string]error{}, volErr: engine.ErrUnsupported, eventsErr: engine.ErrUnsupported, netErr: engine.ErrUnsupported,
		netInspect: `{"Name":"class","Driver":"relay"}`,
		inspect:    `{"Id":"c1","State":{"Status":"exited","Running":false,"ExitCode":3,"FinishedAt":"x"},"Config":{"Env":["B=2","A=1"]},"Mounts":[{"Type":"volume","Name":"data","Destination":"/data","RW":true},{"Source":"/v","Destination":"/ro"}]}`,
		volInspect: `{"Name":"data","Driver":"local"}`}
}

func (f *fake) VolumeCreate(_ context.Context, o api.VolumeCreateOptions) (engine.Volume, error) {
	return engine.Volume{Name: o.Name}, f.record("volume create " + o.Name)
}

func (f *fake) VolumeInspect(_ context.Context, name string) (json.RawMessage, error) {
	err := f.record("volume inspect " + name)
	return json.RawMessage(get(f, func() string { return f.volInspect })), err
}

func (f *fake) VolumeRemove(_ context.Context, name string, force bool) error {
	if force {
		return f.record("volume rm -f " + name)
	}
	return f.record("volume rm " + name)
}

func (f *fake) VolumesPrune(_ context.Context, all bool) (api.VolumePruneResponse, error) {
	err := f.record(fmt.Sprintf("volume prune all=%v", all))
	return get(f, func() api.VolumePruneResponse { return f.prune }), err
}

func (f *fake) Stats(ctx context.Context, id string, stream bool, fn func(engine.Stats)) error {
	f.record(fmt.Sprintf("stats %s stream=%v", id, stream))
	for _, s := range get(f, func() []engine.Stats { return f.stats }) {
		fn(s)
	}
	if stream {
		<-ctx.Done()
		return ctx.Err()
	}
	return get(f, func() error { return f.statsErr })
}

// set changes the fake's state under its lock (background refreshes may
// be reading it).
func (f *fake) set(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

// setErr makes call name fail with err (nil: succeed).
func (f *fake) setErr(name string, err error) {
	f.set(func() {
		if err == nil {
			delete(f.err, name)
		} else {
			f.err[name] = err
		}
	})
}

// get reads the fake's state under its lock.
func get[T any](f *fake, fn func() T) T {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fn()
}

func (f *fake) record(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
	return f.err[name]
}

func (f *fake) called(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == name {
			return true
		}
	}
	return false
}

func (f *fake) Version(context.Context) (api.SystemVersion, error) {
	if g := get(f, func() chan struct{} { return f.gate }); g != nil {
		<-g
	}
	v := api.SystemVersion{Version: "v0.2.0", APIVersion: "1.47", Os: "js", Arch: "wasm"}
	return v, f.record("version")
}

func (f *fake) Containers(_ context.Context, all bool) ([]api.ContainerSummary, error) {
	err := f.record("containers")
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]api.ContainerSummary(nil), f.containers...), err
}

func (f *fake) Images(context.Context) ([]api.ImageSummary, error) {
	err := f.record("images")
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]api.ImageSummary(nil), f.images...), err
}

func (f *fake) InspectContainer(_ context.Context, id string) (json.RawMessage, error) {
	err := f.record("inspect " + id)
	return json.RawMessage(get(f, func() string { return f.inspect })), err
}

func (f *fake) InspectImage(_ context.Context, ref string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), f.record("inspect-image " + ref)
}

func (f *fake) Start(_ context.Context, id string) error { return f.record("start " + id) }
func (f *fake) Kill(_ context.Context, id string) error  { return f.record("kill " + id) }

func (f *fake) Stop(_ context.Context, id string) error    { return f.record("stop " + id) }
func (f *fake) Restart(_ context.Context, id string) error { return f.record("restart " + id) }

func (f *fake) Remove(_ context.Context, id string, force bool) error {
	if force {
		return f.record("rm -f " + id)
	}
	return f.record("rm " + id)
}

func (f *fake) Create(_ context.Context, name string, req api.ContainerCreateRequest) (api.ContainerCreateResponse, error) {
	err := f.record("create " + name + " " + req.Image + " " + strings.Join(req.Cmd, ","))
	f.mu.Lock()
	missing := f.noImage
	f.mu.Unlock()
	if missing && err == nil {
		return api.ContainerCreateResponse{}, &api.ErrDaemon{Status: http.StatusNotFound, Message: "no such image: " + req.Image}
	}
	return api.ContainerCreateResponse{ID: "new1"}, err
}

func (f *fake) Pull(_ context.Context, ref string, progress func(api.JSONMessage)) error {
	if progress != nil {
		for _, m := range get(f, func() []api.JSONMessage { return f.pullMsgs }) {
			progress(m)
		}
	}
	f.mu.Lock()
	f.noImage = false
	f.mu.Unlock()
	return f.record("pull " + ref)
}

func (f *fake) RemoveImage(_ context.Context, ref string, force bool) ([]api.ImageDeleteResponseItem, error) {
	if force {
		return nil, f.record("rmi -f " + ref)
	}
	return nil, f.record("rmi " + ref)
}

// mux frames a stdout and a stderr chunk the way the API does.
func mux(stream byte, s string) string {
	n := len(s)
	return string([]byte{stream, 0, 0, 0, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}) + s
}

func (f *fake) Logs(_ context.Context, id string, follow bool) (io.ReadCloser, error) {
	if err := f.record("logs " + id); err != nil {
		return nil, err
	}
	if e := get(f, func() error { return f.logsErr }); e != nil {
		return io.NopCloser(errReader{e}), nil
	}
	return io.NopCloser(strings.NewReader(get(f, func() string { return f.logs }))), nil
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func (f *fake) Volumes(context.Context) ([]engine.Volume, error) {
	f.record("volumes")
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.volumes, f.volErr
}

func (f *fake) DiskUsage(context.Context) (engine.DiskUsage, error) {
	f.record("df")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.df == nil {
		return engine.DiskUsage{}, engine.ErrUnsupported
	}
	return *f.df, nil
}

// Events serves the current events channel; a closed channel ends the
// stream with eventsErr (nil: a clean end); no channel answers eventsErr.
func (f *fake) Events(ctx context.Context, since int64, fn func(engine.Event)) error {
	f.record("events")
	f.set(func() { f.since = append(f.since, since) })
	events := get(f, func() chan engine.Event { return f.events })
	if events == nil {
		return get(f, func() error { return f.eventsErr })
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-events:
			if !ok {
				return get(f, func() error { return f.eventsErr })
			}
			fn(ev)
		}
	}
}

// harness is an App over a fake, with the UI queue drained by the test.
type harness struct {
	t   *testing.T
	f   *fake
	q   *mvvm.Queue
	app *App
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, f: newFake(), q: mvvm.NewQueue(nil)}
	h.app = NewApp(h.f, NewCLI(cli.RootCmd(cli.Options{})), h.q.Post)
	h.app.SetClock(func() time.Time { return time.Unix(1_700_000_100, 0) })
	return h
}

// until drains the UI queue until cond holds (or fails after 5 s).
func (h *harness) until(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s", what)
		}
		if h.q.Drain() == 0 {
			time.Sleep(time.Millisecond)
		}
	}
}

// refresh runs one refresh to completion.
func (h *harness) refresh() {
	h.t.Helper()
	n := h.app.Refreshed.Get()
	h.app.Refresh()
	h.until("a refresh", func() bool { return h.app.Refreshed.Get() > n })
}

// settle drains the queue until nothing more arrives for a while.
func (h *harness) settle() {
	quiet := 0
	for quiet < 20 {
		if h.q.Drain() == 0 {
			quiet++
			time.Sleep(time.Millisecond)
		} else {
			quiet = 0
		}
	}
}
