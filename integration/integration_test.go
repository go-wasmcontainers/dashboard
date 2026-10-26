// Package integration drives the dashboard's viewmodels against a real
// engine (wazero, in-process), reached through the page<->worker bridge:
// the GUI and the terminal act on the same engine and each sees what the
// other did.
package integration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-wasmcontainers/engine/api"
	"github.com/go-wasmcontainers/engine/cli"
	"github.com/go-wasmcontainers/engine/cli/lineedit"
	"github.com/go-widgets/mvvm"
	"github.com/go-widgets/toolkit/vt"
	"github.com/spf13/cobra"

	"github.com/go-wasmcontainers/dashboard/engine"
	"github.com/go-wasmcontainers/dashboard/internal/testengine"
	"github.com/go-wasmcontainers/dashboard/vm"
)

type rig struct {
	t      *testing.T
	q      *mvvm.Queue
	app    *vm.App
	eng    *engine.HTTP
	screen *vt.Screen
}

func newRig(t *testing.T) *rig {
	te := testengine.New(t)
	opts := cli.Options{Version: "test", DefaultHost: "inproc://engine",
		Client: func(string) (*api.Client, error) { return api.NewClientWith(te.Client, "http://engine"), nil }}
	root := func() *cobra.Command { return cli.RootCmd(opts) }
	r := &rig{t: t, q: mvvm.NewQueue(nil), eng: engine.New(te.Client, "http://engine"), screen: vt.New(120, 30, 2000)}
	r.app = vm.NewApp(r.eng, vm.NewCLI(root()), r.q.Post)
	r.app.Terminal.SetShell(vm.WctrShell{Root: root}, lineedit.WctrCompleter(opts, vm.Builtins))
	r.app.Terminal.Attach(func(s string) { r.screen.WriteString(s) }, func() {})
	return r
}

func (r *rig) until(what string, cond func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	poll := time.Now()
	for !cond() {
		if time.Since(poll) > 100*time.Millisecond {
			r.app.Refresh() // what RunLive's polling does
			poll = time.Now()
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("timed out waiting for %s\nerror: %q\nrows: %v\nterminal:\n%s", what, r.app.Error.Get(), r.app.Containers.Rows.Slice(), r.text())
		}
		if r.q.Drain() == 0 {
			time.Sleep(2 * time.Millisecond)
		}
	}
}

func (r *rig) text() string {
	return r.screen.Text(vt.Pos{}, vt.Pos{Line: r.screen.Lines(), Col: 120})
}

// typeLine types a command in the terminal and waits for its prompt.
func (r *rig) typeLine(line string) {
	r.t.Helper()
	before := r.screen.Lines()
	_, y, _ := r.screen.Cursor()
	mark := r.screen.ScrollbackLen() + y // the prompt line the command is typed on
	r.app.Terminal.Input(line + "\r")
	r.until(line, func() bool {
		_, cy, _ := r.screen.Cursor()
		cur := r.screen.ScrollbackLen() + cy
		return cur > mark && r.lineAt(cur) == "$" && !r.app.Terminal.Running.Get() && r.screen.Lines() >= before
	})
}

func (r *rig) lineAt(i int) string { return r.screen.Text(vt.Pos{Line: i}, vt.Pos{Line: i, Col: 119}) }

func (r *rig) refresh() {
	r.t.Helper()
	n := r.app.Refreshed.Get()
	r.app.Refresh()
	r.until("refresh", func() bool { return r.app.Refreshed.Get() > n && !r.app.Refreshing() && r.q.Len() == 0 })
}

func (r *rig) container(name string) (vm.ContainerRow, bool) {
	c := r.app.Containers
	for i := 0; i < c.Rows.Len(); i++ {
		if c.Rows.At(i).Name == name {
			return c.Rows.At(i), true
		}
	}
	return vm.ContainerRow{}, false
}

func TestTerminalAndGUIShareTheEngine(t *testing.T) {
	r := newRig(t)
	r.refresh()
	if !strings.HasPrefix(r.app.EngineVersion.Get(), "engine test · API 1.47") {
		t.Errorf("version = %q", r.app.EngineVersion.Get())
	}

	// The terminal runs a container; the GUI lists it.
	r.typeLine("wctr run -d --name ticker clock -interval 20ms")
	r.until("ticker listed", func() bool {
		row, ok := r.container("ticker")
		return ok && row.Running()
	})
	if !strings.Contains(r.app.Summary.Get(), "1 running") {
		t.Errorf("summary = %q", r.app.Summary.Get())
	}
	// Tab completes a container name, through the engine.
	r.app.Terminal.Input("wctr logs tic\t")
	r.until("completion", func() bool { return strings.Contains(r.text(), "$ wctr logs ticker") })
	r.app.Terminal.Input("\x15")

	// Its logs follow in the detail pane, ANSI colours and all, as text.
	row, _ := r.container("ticker")
	logs := vt.New(80, 10, 500)
	logs.SetNewlineMode(true)
	r.app.Detail.LogOut = func(b []byte) { logs.Write(b) }
	r.app.Detail.LogReset = func() {}
	r.app.Detail.Open(row.ID)
	r.until("log lines", func() bool { return strings.Contains(logs.Text(vt.Pos{}, vt.Pos{Line: logs.Lines()}), "tick 3") })
	r.until("inspect", func() bool { return r.app.Detail.State.Get() == "running" })

	// The GUI stops it; the terminal shows the command and sees the state.
	r.app.Containers.Stop(row)
	r.until("stopped", func() bool {
		row, ok := r.container("ticker")
		return ok && !row.Running()
	})
	if !strings.Contains(r.text(), "# dashboard: wctr stop ticker") {
		t.Errorf("terminal did not show the action:\n%s", r.text())
	}
	r.typeLine("wctr ps -a")
	if !strings.Contains(r.text(), "ticker") || !strings.Contains(r.text(), "Exited") {
		t.Errorf("ps -a:\n%s", r.text())
	}

	// Restart (start again), then remove (forced while running).
	row, _ = r.container("ticker")
	r.app.Containers.Restart(row)
	r.until("restarted", func() bool {
		row, ok := r.container("ticker")
		return ok && row.Running()
	})
	row, _ = r.container("ticker")
	r.app.Containers.Restart(row) // while running: kill, wait, start
	r.until("restarted while running", func() bool { return r.app.Pending.Get() == 0 })
	if r.app.Error.Get() != "" {
		t.Fatalf("restart: %s", r.app.Error.Get())
	}
	r.until("running again", func() bool {
		row, ok := r.container("ticker")
		return ok && row.Running()
	})
	row, _ = r.container("ticker")
	r.app.Containers.Remove(row)
	r.until("removed", func() bool { _, ok := r.container("ticker"); return !ok })
	r.until("detail closed", func() bool { return r.app.Detail.ID.Get() == "" })
	if r.app.Error.Get() != "" {
		t.Errorf("error = %q", r.app.Error.Get())
	}

	// The images view: pull, run from the dialog, remove.
	im := r.app.Images
	im.PullRef.Set("hello")
	im.Pull()
	r.until("pulled", func() bool { return !im.Pulling.Get() && im.PullLog.Len() > 0 })
	if last := im.PullLog.At(im.PullLog.Len() - 1); last != "done" {
		t.Fatalf("pull log ends with %q", last)
	}
	r.until("hello listed", func() bool {
		for i := 0; i < im.Rows.Len(); i++ {
			if im.Rows.At(i).Ref == "hello:latest" {
				return true
			}
		}
		return false
	})
	im.Run.Reset("fail")
	im.Run.Name.Set("broken")
	if !im.Run.Start() {
		t.Fatal(im.Run.Error.Get())
	}
	r.until("fail exited", func() bool {
		row, ok := r.container("broken")
		n, exited := row.ExitCode()
		return ok && exited && n == 3
	})
	im.Run.Reset("hello")
	im.Run.Name.Set("greeter")
	im.Run.Env.Set("GREETING=bonjour")
	im.Run.Command.Set("student")
	im.Run.Start()
	r.until("greeter ran", func() bool { row, ok := r.container("greeter"); return ok && !row.Running() })
	r.typeLine("wctr logs greeter")
	if !strings.Contains(r.text(), "Hello, student!") || !strings.Contains(r.text(), "GREETING = bonjour") {
		t.Errorf("logs:\n%s", r.text())
	}
	r.typeLine("wctr rm greeter broken")
	r.until("gone after the terminal's rm", func() bool { _, a := r.container("greeter"); _, b := r.container("broken"); return !a && !b })
	for i := 0; i < im.Rows.Len(); i++ {
		if im.Rows.At(i).Ref == "hello:latest" {
			im.Remove(im.Rows.At(i), false)
		}
	}
	r.until("hello removed", func() bool {
		for i := 0; i < im.Rows.Len(); i++ {
			if im.Rows.At(i).Ref == "hello:latest" {
				return false
			}
		}
		return true
	})
}

func TestUnservedEndpointsAreUnsupported(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if _, err := r.eng.Volumes(ctx); !errors.Is(err, engine.ErrUnsupported) {
		t.Errorf("Volumes: %v", err)
	}
	// Disk usage is served since engine v0.4.0, with the engine's Storage.
	if df, err := r.eng.DiskUsage(ctx); err != nil || df.Storage == nil {
		t.Errorf("DiskUsage: %+v %v", df, err)
	}
	if err := r.eng.Events(ctx, func(engine.Event) {}); !errors.Is(err, engine.ErrUnsupported) {
		t.Errorf("Events: %v", err)
	}
	// An API 404 (JSON) is an ordinary error, not "unsupported".
	if _, err := r.eng.InspectContainer(ctx, "nope"); err == nil || errors.Is(err, engine.ErrUnsupported) || !api.IsNotFound(err) {
		t.Errorf("Inspect: %v", err)
	}
	r.refresh()
	if r.app.Volumes.Supported.Get() != vm.VolumesUnsupported || !strings.Contains(r.app.Volumes.Message.Get(), "engine test does not yet") {
		t.Errorf("volumes: %q", r.app.Volumes.Message.Get())
	}
	if !strings.HasPrefix(r.app.Storage.Get(), "storage 0B · ") {
		t.Errorf("storage = %q", r.app.Storage.Get())
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { r.app.RunLive(ctx, 10*time.Millisecond); close(done) }()
	r.until("polling", func() bool {
		return strings.HasPrefix(r.app.Live.Get(), "polling every 10ms (the engine serves no GET /events)")
	})
	cancel()
	<-done
}

// TestManyContainers fills the engine with 200 containers and checks the
// view keeps all of them, and that a refresh with nothing changed emits no
// list event (no redraw).
func TestManyContainers(t *testing.T) {
	r := newRig(t)
	now := time.Now().Add(time.Hour) // a still clock: "Created" ages do not tick
	r.app.SetClock(func() time.Time { return now })
	r.typeLine("wctr pull hello")
	ctx := context.Background()
	for i := 0; i < 200; i++ {
		if _, err := r.eng.Create(ctx, "", api.ContainerCreateRequest{ContainerConfig: api.ContainerConfig{Image: "hello"}}); err != nil {
			t.Fatal(err)
		}
	}
	r.refresh()
	if r.app.Containers.Rows.Len() != 200 {
		t.Fatalf("rows = %d, error %q\n%s", r.app.Containers.Rows.Len(), r.app.Error.Get(), r.text())
	}
	events := 0
	r.app.Containers.Rows.Subscribe(func(mvvm.ListEvent[vm.ContainerRow]) { events++ })
	r.refresh()
	if events != 0 {
		t.Errorf("an unchanged refresh emitted %d list events", events)
	}
}
