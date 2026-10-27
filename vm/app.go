// Package vm holds the dashboard's viewmodels: pure Go state over the
// engine.Engine interface, natively testable, with no widget in sight.
// The views (package ui) bind to the observables here and call the
// actions; every action goes through the engine's API, so the terminal
// and any other client see its effect, and each one names the wctr
// command line it stands for.
//
// Threading: observables are touched on one goroutine only, the UI's.
// Work that waits on the engine runs on its own goroutine and hands its
// result back through the post function the App is given (an mvvm.Queue's
// Post in the browser, drained at the start of each frame).
package vm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-wasmcontainers/engine/api"
	"github.com/go-widgets/mvvm"

	"github.com/go-wasmcontainers/dashboard/engine"
)

// The views, in navigation order.
const (
	ViewContainers = iota
	ViewImages
	ViewVolumes
	ViewNetworks
	ViewTerminal
	NumViews
)

// ViewNames are the navigation labels.
var ViewNames = [NumViews]string{"Containers", "Images", "Volumes", "Networks", "Terminal"}

// PollInterval is how often the dashboard refreshes when the engine has
// no event stream.
const PollInterval = 2 * time.Second

// App is the dashboard's root viewmodel.
type App struct {
	eng  engine.Engine
	cli  *CLI
	post func(func())
	now  func() time.Time

	// View is the navigation selection (ViewContainers ...).
	View *mvvm.Observable[int]
	// LastCommand is the wctr command line the last action stands for.
	LastCommand *mvvm.Observable[string]
	// Error is the last failure, "" when the last action succeeded.
	Error *mvvm.Observable[string]
	// EngineVersion, Summary and Storage feed the status bar.
	EngineVersion *mvvm.Observable[string]
	Summary       *mvvm.Observable[string]
	Storage       *mvvm.Observable[string]
	// Live says how the dashboard keeps up: "live (events)" or polling.
	Live *mvvm.Observable[string]

	Containers *Containers
	Images     *Images
	Volumes    *Volumes
	Networks   *Networks
	Class      *Class
	Detail     *Detail
	Terminal   *Terminal

	mu         sync.Mutex
	refreshing bool
	again      bool
	// Refreshed counts completed refreshes (tests wait on it).
	Refreshed *mvvm.Observable[int]
	// Pending counts actions under way (the view shows it busy).
	Pending *mvvm.Observable[int]
}

// NewApp builds the viewmodels over eng. cli is the wctr command tree (for
// the command lines shown), post runs a function on the UI goroutine.
func NewApp(eng engine.Engine, cli *CLI, post func(func())) *App {
	a := &App{
		eng: eng, cli: cli, post: post, now: time.Now,
		View:          mvvm.NewObservable(ViewContainers),
		LastCommand:   mvvm.NewObservable(""),
		Error:         mvvm.NewObservable(""),
		EngineVersion: mvvm.NewObservable("engine: connecting…"),
		Summary:       mvvm.NewObservable(""),
		Storage:       mvvm.NewObservable(""),
		Live:          mvvm.NewObservable(""),
		Refreshed:     mvvm.NewObservable(0),
		Pending:       mvvm.NewObservable(0),
	}
	a.Containers = newContainers(a)
	a.Images = newImages(a)
	a.Volumes = newVolumes(a)
	a.Networks = newNetworks(a)
	a.Class = newClass(a)
	a.Detail = newDetail(a)
	a.Terminal = newTerminal(a)
	return a
}

// CLI is the wctr command tree the command lines are checked against.
func (a *App) CLI() *CLI { return a.cli }

// SetClock replaces the clock (tests).
func (a *App) SetClock(now func() time.Time) { a.now = now }

// snapshot is what one refresh reads from the engine.
type snapshot struct {
	version    api.SystemVersion
	containers []api.ContainerSummary
	images     []api.ImageSummary
	volumes    []engine.Volume
	volErr     error
	networks   []engine.Network
	netErr     error
	df         *engine.DiskUsage
	err        error
}

// Refresh re-reads the engine in the background and applies what changed.
// Calls while one is running coalesce into one more.
func (a *App) Refresh() {
	a.mu.Lock()
	if a.refreshing {
		a.again = true
		a.mu.Unlock()
		return
	}
	a.refreshing = true
	a.mu.Unlock()
	go a.refreshLoop()
}

// Refreshing reports whether a refresh is under way (or queued).
func (a *App) Refreshing() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.refreshing
}

func (a *App) refreshLoop() {
	for {
		s := a.read(context.Background())
		a.post(func() { a.apply(s) })
		a.mu.Lock()
		if !a.again {
			a.refreshing = false
			a.mu.Unlock()
			return
		}
		a.again = false
		a.mu.Unlock()
	}
}

func (a *App) read(ctx context.Context) snapshot {
	var s snapshot
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if s.version, s.err = a.eng.Version(ctx); s.err != nil {
		return s
	}
	if s.containers, s.err = a.eng.Containers(ctx, true); s.err != nil {
		return s
	}
	if s.images, s.err = a.eng.Images(ctx); s.err != nil {
		return s
	}
	s.volumes, s.volErr = a.eng.Volumes(ctx)
	s.networks, s.netErr = a.eng.Networks(ctx)
	if df, err := a.eng.DiskUsage(ctx); err == nil {
		s.df = &df
	}
	return s
}

func (a *App) apply(s snapshot) {
	defer a.Refreshed.Set(a.Refreshed.Get() + 1)
	if s.err != nil {
		a.EngineVersion.Set("engine: unreachable")
		a.Error.Set(Clean(s.err.Error(), 500))
		return
	}
	a.EngineVersion.Set(Clean(fmt.Sprintf("engine %s · API %s · %s/%s", s.version.Version, s.version.APIVersion, s.version.Os, s.version.Arch), 120))
	now := a.now()
	a.Containers.apply(s.containers, now)
	a.Images.apply(s.images, now)
	a.Volumes.apply(s.volumes, s.volErr, s.version.Version, s.df)
	a.Networks.apply(s.networks, s.netErr, s.version.Version)
	a.Detail.refresh()
	running := 0
	for _, c := range s.containers {
		if c.State == "running" {
			running++
		}
	}
	a.Summary.Set(fmt.Sprintf("%d running · %d containers · %d images", running, len(s.containers), len(s.images)))
	if s.df != nil {
		var total int64 = s.df.LayersSize
		for _, v := range s.df.Volumes {
			if v.UsageData != nil && v.UsageData.Size > 0 {
				total += v.UsageData.Size
			}
		}
		a.Storage.Set(storageLine(total, s.df.Storage))
		return
	}
	var total int64
	for _, im := range s.images {
		total += im.Size
	}
	a.Storage.Set("images " + Size(total) + " (disk usage requires GET /system/df)")
}

// storageLine is the status bar's storage segment: what the images and
// volumes take (GET /system/df), and where the engine keeps its state and
// how full that is, when the engine says (its Storage addition).
func storageLine(total int64, st *api.StorageUsage) string {
	s := "storage " + Size(total)
	if st == nil {
		return s
	}
	s += " · " + Clean(st.Kind, 60)
	switch {
	case st.Usage >= 0 && st.Quota > 0:
		s += fmt.Sprintf(" %s of %s", Size(st.Usage), Size(st.Quota))
	case st.Usage >= 0:
		s += " " + Size(st.Usage)
	}
	if st.Persistent {
		s += " (persistent)"
	}
	if st.Error != "" {
		s += " · error: " + Clean(st.Error, 120)
	}
	return s
}

// Do runs an action against the engine in the background: line is the
// wctr command line it stands for (shown, and echoed in the terminal when
// asked), fn the API calls. The dashboard refreshes when it is done.
func (a *App) Do(line string, fn func(ctx context.Context) error) {
	a.LastCommand.Set(line)
	a.Error.Set("")
	a.Terminal.note(line)
	a.Pending.Set(a.Pending.Get() + 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		err := fn(ctx)
		a.post(func() {
			a.Pending.Set(a.Pending.Get() - 1)
			if err != nil {
				a.Error.Set(Clean(err.Error(), 500))
			}
			a.Refresh()
		})
	}()
}

// Reconnection delays of the event stream: the first retry, and the most
// it backs off to while the stream keeps ending without an event.
var (
	ReconnectDelay    = 200 * time.Millisecond
	MaxReconnectDelay = 5 * time.Second
)

// RunLive keeps the dashboard current until ctx ends. It follows the
// engine's event stream (GET /events), refreshing on each event. When the
// stream ends — the engine cuts off a slow reader, or the connection
// breaks — it resynchronises (a full refresh) and reconnects from the time
// of the last event it saw, backing off while the stream keeps failing.
// An engine that serves no event stream is polled every interval instead.
func (a *App) RunLive(ctx context.Context, interval time.Duration) {
	a.Refresh()
	var since int64
	delay := ReconnectDelay
	for ctx.Err() == nil {
		a.post(func() { a.Live.Set("live (GET /events)") })
		got := false
		err := a.eng.Events(ctx, since, func(ev engine.Event) {
			got = true
			if ev.Time > since {
				since = ev.Time
			}
			a.Refresh()
		})
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, engine.ErrUnsupported) {
			a.post(func() { a.Live.Set(fmt.Sprintf("polling every %s (the engine serves no GET /events)", interval)) })
			if !a.poll(ctx, interval) {
				return
			}
			continue
		}
		why := "the stream ended"
		if err != nil {
			why = Clean(err.Error(), 80)
		}
		a.post(func() { a.Live.Set("reconnecting to GET /events (" + why + ")") })
		a.Refresh() // resynchronise: events may have been lost
		if since == 0 {
			since = a.now().Unix()
		}
		if got {
			delay = ReconnectDelay
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, MaxReconnectDelay)
	}
}

// poll refreshes every interval until ctx ends (false); after a few
// rounds it tries the event stream again (true).
func (a *App) poll(ctx context.Context, interval time.Duration) bool {
	t := time.NewTicker(interval)
	defer t.Stop()
	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
			a.Refresh()
		}
		if i == 30 {
			return true // see whether the engine serves events now
		}
	}
}
