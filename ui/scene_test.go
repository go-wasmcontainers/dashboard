package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-wasmcontainers/engine/api"
	"github.com/go-wasmcontainers/engine/cli"
	"github.com/go-wasmcontainers/engine/cli/lineedit"
	"github.com/go-widgets/mvvm"
	"github.com/go-widgets/toolkit"
	"github.com/go-widgets/webcanvas"
	"github.com/spf13/cobra"

	"github.com/go-wasmcontainers/dashboard/engine"
	"github.com/go-wasmcontainers/dashboard/internal/testengine"
	"github.com/go-wasmcontainers/dashboard/vm"
)

var (
	_ webcanvas.App           = (*Scene)(nil)
	_ webcanvas.Resizer       = (*Scene)(nil)
	_ webcanvas.Scroller      = (*Scene)(nil)
	_ webcanvas.RepaintAware  = (*Scene)(nil)
	_ webcanvas.ModifierAware = (*Scene)(nil)
)

type rig struct {
	t   *testing.T
	s   *Scene
	app *vm.App
	eng *engine.HTTP
	buf []byte
}

func newRig(t *testing.T) *rig {
	t.Helper()
	te := testengine.New(t)
	opts := cli.Options{Version: "test", Client: func(string) (*api.Client, error) { return api.NewClientWith(te.Client, "http://engine"), nil }}
	q := mvvm.NewQueue(nil)
	eng := engine.New(te.Client, "http://engine")
	app := vm.NewApp(eng, vm.NewCLI(cli.RootCmd(opts)), q.Post)
	app.Terminal.SetShell(vm.WctrShell{Root: func() *cobra.Command { return cli.RootCmd(opts) }}, lineedit.WctrCompleter(opts, vm.Builtins))
	r := &rig{t: t, app: app, eng: eng}
	r.s = New(app, q, 1280, 800)
	r.buf = make([]byte, 4*1280*800)
	app.Refresh()
	return r
}

func (r *rig) frame() { r.s.Draw(r.buf) }

func (r *rig) until(what string, cond func(Probe) bool) Probe {
	r.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	poll := time.Now()
	for {
		r.frame()
		p := r.s.Probe()
		if cond(p) {
			return p
		}
		if time.Since(poll) > 100*time.Millisecond {
			r.app.Refresh()
			poll = time.Now()
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("timed out waiting for %s: %+v", what, p)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// click clicks the middle of a named control.
func (r *rig) click(name string) {
	r.t.Helper()
	b, ok := r.s.Probe().Boxes[name]
	if !ok {
		r.t.Fatalf("no %s on screen (view %s)", name, r.s.Probe().View)
	}
	x, y := b.X+b.W/2, b.Y+b.H/2
	r.s.Click(x, y)
	r.s.Release(x, y)
}

func (r *rig) typeText(s string) {
	for _, c := range s {
		r.s.Char(string(c))
	}
}

// clickRow clicks body row i of a table.
func (r *rig) clickRow(t *toolkit.Table, i int) {
	r.t.Helper()
	b := t.Bounds()
	for y := 0; y < b.H; y++ {
		if t.RowAt(10, y) == i {
			r.s.Click(b.X+10, b.Y+y+2)
			r.s.Release(b.X+10, b.Y+y+2)
			return
		}
	}
	r.t.Fatalf("row %d not on screen", i)
}

func (r *rig) imageRow(ref string) int {
	r.t.Helper()
	for i, im := range r.s.Probe().Images {
		if im == ref {
			return i
		}
	}
	r.t.Fatalf("no image %s", ref)
	return -1
}

func TestDashboard(t *testing.T) {
	r := newRig(t)
	p := r.until("engine", func(p Probe) bool { return strings.HasPrefix(p.Status[0], "engine test") })
	if p.View != "Containers" || p.Boxes["containers.table"].W == 0 {
		t.Fatalf("first view: %+v", p)
	}
	// Terminal: navigate with the keyboard, type a command.
	r.s.KeyDown("F6") // the navigation
	r.s.Modifiers(webcanvas.Modifiers{Alt: true})
	r.s.KeyDown("4")
	r.s.Modifiers(webcanvas.Modifiers{})
	if r.s.Probe().View != "Terminal" {
		t.Fatalf("Alt+4: %s", r.s.Probe().View)
	}
	r.click("terminal")
	r.typeText("wctr run -d --name web clock -interval 20ms")
	r.s.KeyDown("Enter")
	r.until("web listed", func(p Probe) bool { return len(p.Containers) == 1 && p.Containers[0] == "web" && !p.Running })
	// Alt+1 belongs to the focused terminal; F6 leaves it.
	r.s.Modifiers(webcanvas.Modifiers{Alt: true})
	r.s.KeyDown("1")
	r.s.Modifiers(webcanvas.Modifiers{})
	if r.s.Probe().View != "Terminal" {
		t.Fatal("a global shortcut stole a key from the terminal")
	}
	r.s.KeyDown("F6")      // terminal -> navigation
	r.s.KeyDown("ArrowUp") // nav: Terminal -> Volumes
	r.s.KeyDown("ArrowUp")
	r.s.KeyDown("ArrowUp")
	if v := r.s.Probe().View; v != "Containers" {
		t.Fatalf("arrows in the navigation: %s", v)
	}

	// The GUI: select web, stop it; the hint and the terminal show the command.
	r.clickRow(r.s.cTable, 0)
	r.until("logs", func(p Probe) bool { return strings.Contains(p.Logs, "tick 2") })
	b := r.s.Probe().Boxes["containers.stop"]
	r.s.Move(b.X+5, b.Y+5)
	if h := r.s.Probe().Hint; h != "Stop = wctr stop web" {
		t.Errorf("hover hint = %q", h)
	}
	r.s.Move(b.X+5, b.Y+5) // the same button: nothing new
	r.click("containers.stop")
	r.until("stopped", func(p Probe) bool { return p.Pending == 0 && strings.Contains(p.Status[1], "0 running") })
	if r.s.Probe().LastCommand != "wctr stop web" {
		t.Errorf("last command = %q", r.s.Probe().LastCommand)
	}
	r.click("containers.start")
	r.until("started", func(p Probe) bool { return p.Pending == 0 && strings.Contains(p.Status[1], "1 running") })
	r.click("containers.restart")
	r.until("restarted", func(p Probe) bool { return p.Pending == 0 && strings.Contains(p.Status[1], "1 running") })
	// Detail tabs and the follow toggle; the Stats tab gets readings.
	d := r.s.detail.Bounds()
	r.s.Click(d.X+120, d.Y+8)
	r.s.Release(d.X+120, d.Y+8)
	r.frame()
	r.s.detail.Active().Set(4) // Stats
	r.until("stats", func(p Probe) bool { return p.Memory > 0 && p.CPU == "--" && len(r.s.stats.spark.Values) > 0 })
	r.frame()
	r.s.detail.Active().Set(0)
	r.click("detail.follow")
	r.frame()
	// Search filters; the check shows running only.
	r.click("containers.search")
	r.typeText("zzz")
	r.until("filtered", func(p Probe) bool { return len(p.Containers) == 0 })
	r.s.KeyDown("Backspace")
	r.s.KeyDown("Backspace")
	r.s.KeyDown("Backspace")
	r.until("unfiltered", func(p Probe) bool { return len(p.Containers) == 1 })
	r.click("containers.all")
	r.frame()
	r.click("containers.remove")
	r.until("removed", func(p Probe) bool { return len(p.Containers) == 0 && p.Pending == 0 })
	r.click("containers.all")

	// Images: pull from the panel, then run from the run panel.
	r.s.KeyDown("F6")
	r.s.Modifiers(webcanvas.Modifiers{Alt: true})
	r.s.KeyDown("2")
	r.s.Modifiers(webcanvas.Modifiers{})
	r.click("images.pull")
	r.typeText("hello")
	if h := r.s.buttonLine(r.s.pullGo); h != "Pull = wctr pull hello" {
		t.Errorf("pull line = %q", h)
	}
	r.click("pull.go")
	r.until("pulled", func(p Probe) bool { return len(p.Images) == 2 && p.Pending == 0 })
	r.click("panel.close")
	r.clickRow(r.s.iTable, r.imageRow("hello:latest"))
	r.click("images.run")
	if r.s.Probe().Focus != "run.image" {
		t.Errorf("focus = %q", r.s.Probe().Focus)
	}
	r.s.KeyDown("Tab") // to the name
	r.typeText("greeter")
	r.s.Modifiers(webcanvas.Modifiers{Shift: true})
	r.s.KeyDown("Tab")
	r.s.Modifiers(webcanvas.Modifiers{})
	if r.s.Probe().Focus != "run.image" {
		t.Errorf("Shift+Tab focus = %q", r.s.Probe().Focus)
	}
	if h := r.s.buttonLine(r.s.runGo); h != "Run = wctr run -d --name greeter hello:latest" {
		t.Errorf("run line = %q", h)
	}
	r.click("run.go")
	r.until("greeter ran", func(p Probe) bool { return p.View == "Containers" && len(p.Containers) == 1 && p.Pending == 0 })
	r.app.View.Set(vm.ViewImages)
	r.clickRow(r.s.iTable, r.imageRow("hello:latest"))
	r.click("images.force")
	if h := r.s.buttonLine(r.s.iRemove); h != "Remove = wctr rmi -f hello:latest" {
		t.Errorf("rmi line = %q", h)
	}
	r.click("images.remove")
	r.until("image removed", func(p Probe) bool { return len(p.Images) == 1 && p.Images[0] == "clock:latest" && p.Pending == 0 })
	if r.s.buttonLine(r.s.iRemove) != "" || r.s.buttonLine(r.s.themeBtn) != "" {
		t.Error("a line for a button that stands for nothing")
	}

	// Volumes: create from the field, select, inspect, remove, prune.
	r.app.View.Set(vm.ViewVolumes)
	p = r.until("no volumes", func(p Probe) bool { return strings.HasPrefix(p.Volumes, "No volumes.") })
	r.click("volumes.name")
	r.typeText("data")
	if h := r.s.buttonLine(r.s.vCreate); h != "Create = wctr volume create data" {
		t.Errorf("create line = %q", h)
	}
	r.click("volumes.create")
	r.until("created", func(p Probe) bool { return len(p.VolumeNames) == 1 && p.Pending == 0 })
	r.clickRow(r.s.vTable, 0)
	r.until("inspect", func(p Probe) bool { return len(r.s.vInspect.Items) > 1 })
	r.click("volumes.force")
	if h := r.s.buttonLine(r.s.vRemove); h != "Remove = wctr volume rm -f data" {
		t.Errorf("rm line = %q", h)
	}
	r.click("volumes.all")
	if h := r.s.buttonLine(r.s.vPrune); h != "Prune = wctr volume prune -a -f" {
		t.Errorf("prune line = %q", h)
	}
	r.click("volumes.remove")
	r.until("removed", func(p Probe) bool { return len(p.VolumeNames) == 0 && p.Pending == 0 })
	r.click("volumes.prune")
	r.until("pruned", func(p Probe) bool { return p.Pending == 0 && strings.HasPrefix(r.s.vNotice.Text().Get(), "pruned") })
	if r.s.buttonLine(r.s.vRemove) != "" {
		t.Error("a remove line with nothing selected")
	}
	r.s.Scroll(p.Boxes["volumes.table"].X+5, p.Boxes["volumes.table"].Y+5, 0, 1)
	if r.s.Scroll(-10, -10, 0, 1) {
		t.Error("a scroll outside every widget")
	}

	// Theme, resize, context click, a click on nothing.
	r.click("theme")
	if !r.s.Probe().Dark {
		t.Error("dark theme")
	}
	r.frame()
	r.click("theme")
	if w, h := r.s.Resize(100, 100); w != minW || h != minH {
		t.Errorf("Resize = %d %d", w, h)
	}
	r.buf = make([]byte, 4*minW*minH)
	r.frame()
	if w, h := r.s.Size(); w != minW || h != minH {
		t.Error("Size")
	}
	if r.s.Click(r.s.w-1, headerH+gap+1) || r.s.Release(1, 1) {
		t.Error("a click on nothing")
	}
	r.s.Context(p.Boxes["nav"].X+5, p.Boxes["nav"].Y+5)
	if !r.s.Dirty() {
		t.Error("not dirty after a click")
	}
	var asked int
	r.s.RepaintWith(func() { asked++ })
	r.s.invalidate()
	if asked != 1 {
		t.Error("repaint not requested")
	}
}

func TestKeysWithoutFocus(t *testing.T) {
	r := newRig(t)
	if r.s.Char("x") || r.s.KeyDown("Enter") {
		t.Error("keys with nothing focused")
	}
	r.s.cycleFocus(false, false) // from nothing, backwards
	if r.s.focus == nil {
		t.Error("no focus")
	}
	if r.s.Move(-5, -5) || r.s.Move(-6, -6) {
		t.Error("hover on nothing twice")
	}
}

func TestStateColor(t *testing.T) {
	for _, c := range []struct {
		state string
		code  int
		ex    bool
		ok    bool
	}{{"running", 0, false, true}, {"exited", 3, true, true}, {"exited", 0, true, true}, {"created", 0, false, true},
		{"paused", 0, false, true}, {"weird", 0, false, false}} {
		if _, ok := StateColor(c.state, c.code, c.ex); ok != c.ok {
			t.Errorf("StateColor(%s) ok = %v", c.state, ok)
		}
	}
	red, _ := StateColor("exited", 3, true)
	grey, _ := StateColor("exited", 0, true)
	if red == grey {
		t.Error("a failed exit looks like a clean one")
	}
}

// BenchmarkFrame draws a full frame with 200 containers and 100 images
// (one core): the per-frame cost when the dashboard has to repaint.
func BenchmarkFrame(b *testing.B) {
	var names []string
	for i := 0; i < 99; i++ {
		names = append(names, "img"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	te := testengine.New(b, names...)
	eng := engine.New(te.Client, "http://engine")
	q := mvvm.NewQueue(nil)
	app := vm.NewApp(eng, vm.NewCLI(cli.RootCmd(cli.Options{})), q.Post)
	ctx := context.Background()
	for _, n := range append(names, "hello") {
		if err := eng.Pull(ctx, n, nil); err != nil {
			b.Fatal(err)
		}
	}
	for i := 0; i < 200; i++ {
		if _, err := eng.Create(ctx, "", api.ContainerCreateRequest{ContainerConfig: api.ContainerConfig{Image: "hello"}}); err != nil {
			b.Fatal(err)
		}
	}
	s := New(app, q, 1280, 800)
	n := app.Refreshed.Get()
	app.Refresh()
	buf := make([]byte, 4*1280*800)
	for app.Refreshed.Get() == n {
		s.Draw(buf)
		time.Sleep(time.Millisecond)
	}
	if app.Containers.Rows.Len() != 200 || app.Images.Rows.Len() != 100 {
		b.Fatalf("%d containers, %d images", app.Containers.Rows.Len(), app.Images.Rows.Len())
	}
	for _, view := range []int{vm.ViewContainers, vm.ViewImages, vm.ViewTerminal} {
		app.View.Set(view)
		name := vm.ViewNames[view]
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				s.Draw(buf)
			}
		})
	}
}
