//go:build e2e

// Package e2e drives the demo page in real browsers (Chromium and Firefox,
// through Playwright): real clicks on the canvas and real key presses, with
// the state read back through window.__dashboard.state() — never pixels.
//
//	go test -tags e2e ./e2e/
//
// (browsers: go run github.com/playwright-community/playwright-go/cmd/playwright install chromium firefox).
// E2E_URL=<url> runs against a deployed page instead of a local build.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"

	"github.com/go-wasmcontainers/dashboard/internal/site"
	"github.com/go-wasmcontainers/dashboard/ui"
)

var siteDir string

func TestMain(m *testing.M) {
	if os.Getenv("E2E_URL") != "" {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "dashboard-e2e-")
	if err != nil {
		panic(err)
	}
	siteDir = filepath.Join(dir, "site")
	if _, err := site.Build(context.Background(), site.Options{Root: "..", Out: siteDir, Version: "e2e", Log: os.Stderr}); err != nil {
		fmt.Fprintln(os.Stderr, "site build:", err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func serve(t *testing.T, isolate bool) string {
	if u := os.Getenv("E2E_URL"); u != "" {
		return u
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: site.Handler(siteDir, "/dashboard/", isolate)}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return "http://" + l.Addr().String() + "/dashboard/"
}

type driver struct {
	t    *testing.T
	page playwright.Page
	log  *[]string
}

func (d *driver) state() ui.Probe {
	d.t.Helper()
	v, err := d.page.Evaluate("() => window.__dashboard ? window.__dashboard.state() : ''")
	if err != nil {
		d.t.Fatalf("state: %v", err)
	}
	p := ui.Probe{Status: make([]string, 4)}
	if s, _ := v.(string); s != "" {
		if err := json.Unmarshal([]byte(s), &p); err != nil {
			d.t.Fatal(err)
		}
	}
	return p
}

func (d *driver) until(what string, timeout time.Duration, cond func(ui.Probe) bool) ui.Probe {
	d.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		p := d.state()
		if cond(p) {
			return p
		}
		if time.Now().After(deadline) {
			d.t.Fatalf("timed out waiting for %s\nstate: %+v\nconsole: %s", what, p, strings.Join(*d.log, "\n"))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// click clicks the middle of a named control, in canvas pixels (the
// canvas fills the viewport at its intrinsic size).
func (d *driver) click(name string) {
	d.t.Helper()
	b, ok := d.state().Boxes[name]
	if !ok {
		d.t.Fatalf("no %s on screen", name)
	}
	if err := d.page.Mouse().Click(float64(b.X+b.W/2), float64(b.Y+b.H/2)); err != nil {
		d.t.Fatal(err)
	}
}

func (d *driver) press(keys ...string) {
	d.t.Helper()
	for _, k := range keys {
		if err := d.page.Keyboard().Press(k); err != nil {
			d.t.Fatal(err)
		}
	}
}

func session(t *testing.T, pw *playwright.Playwright, browser string, isolate bool) {
	bt := pw.Chromium
	if browser == "firefox" {
		bt = pw.Firefox
	}
	b, err := bt.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true), Timeout: playwright.Float(180000)})
	if err != nil {
		t.Fatalf("launch %s: %v", browser, err)
	}
	defer b.Close()
	page, err := b.NewPage(playwright.BrowserNewPageOptions{Viewport: &playwright.Size{Width: 1280, Height: 800}})
	if err != nil {
		t.Fatal(err)
	}
	var log []string
	page.On("console", func(m playwright.ConsoleMessage) { log = append(log, m.Text()) })
	page.On("pageerror", func(err error) { log = append(log, "pageerror: "+err.Error()) })
	if _, err := page.Goto(serve(t, isolate)); err != nil {
		t.Fatal(err)
	}
	d := &driver{t: t, page: page, log: &log}
	t0 := time.Now()
	d.until("the dashboard", 90*time.Second, func(p ui.Probe) bool { return strings.HasPrefix(p.Status[0], "engine e2e") })
	t.Logf("%s: dashboard ready in %s", browser, time.Since(t0).Round(time.Millisecond))

	// Keyboard: F6 to the navigation, down to Terminal; type a run.
	d.press("F6", "ArrowDown", "ArrowDown", "ArrowDown")
	d.until("terminal view", 5*time.Second, func(p ui.Probe) bool { return p.View == "Terminal" })
	d.click("terminal")
	// One key press per character, as a person types (Playwright's Type
	// inserts text without the keydown events a canvas listens to).
	for _, c := range "wctr run -d --name web clock -interval 100ms" {
		k := string(c)
		if c == ' ' {
			k = "Space"
		}
		d.press(k)
	}
	d.press("Enter")
	d.until("web running", 60*time.Second, func(p ui.Probe) bool {
		return len(p.Containers) == 1 && p.Containers[0] == "web" && strings.Contains(p.Status[1], "1 running")
	})
	// Ctrl+L belongs to the terminal (it clears it), not to the browser.
	d.press("Control+l")
	// Back to the containers: select web with the keyboard, stop it with a click.
	d.press("F6", "ArrowUp", "ArrowUp", "ArrowUp")
	d.until("containers view", 5*time.Second, func(p ui.Probe) bool { return p.View == "Containers" })
	d.press("F6", "ArrowDown")
	d.until("web selected", 5*time.Second, func(p ui.Probe) bool { return p.Selected != "" })
	d.until("logs", 30*time.Second, func(p ui.Probe) bool { return strings.Contains(p.Logs, "tick 2") })
	d.click("containers.stop")
	p := d.until("stopped", 30*time.Second, func(p ui.Probe) bool { return strings.Contains(p.Status[1], "0 running") && p.Pending == 0 })
	if p.LastCommand != "wctr kill web" {
		t.Errorf("last command = %q", p.LastCommand)
	}
	// The terminal shows what the button did.
	d.press("F6", "ArrowDown", "ArrowDown", "ArrowDown")
	d.until("the action in the terminal", 10*time.Second, func(p ui.Probe) bool {
		return strings.Contains(p.Terminal, "# dashboard: wctr kill web")
	})
}

func TestBrowsers(t *testing.T) {
	pw, err := playwright.Run()
	if err != nil {
		t.Fatalf("playwright: %v", err)
	}
	defer pw.Stop()
	for _, c := range []struct {
		browser string
		isolate bool
	}{{"chromium", true}, {"chromium", false}, {"firefox", true}} {
		name := c.browser + map[bool]string{true: "/headers", false: "/service-worker"}[c.isolate]
		t.Run(name, func(t *testing.T) { session(t, pw, c.browser, c.isolate) })
	}
}
