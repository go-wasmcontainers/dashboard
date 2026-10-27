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
	p := ui.Probe{Status: make([]string, 4)}
	v, err := d.page.Evaluate("() => window.__dashboard ? window.__dashboard.state() : ''")
	if err != nil {
		// The service worker reloads the page once to isolate it.
		*d.log = append(*d.log, "state: "+err.Error())
		return p
	}
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
	d.until("the dashboard", 90*time.Second, func(p ui.Probe) bool {
		return strings.HasPrefix(p.Status[0], "engine ") && !strings.HasPrefix(p.Status[0], "engine: ")
	})
	t.Logf("%s: dashboard ready in %s", browser, time.Since(t0).Round(time.Millisecond))

	// Keyboard: F6 to the navigation, down to Terminal; type a run.
	d.press("F6", "ArrowDown", "ArrowDown", "ArrowDown", "ArrowDown")
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
	d.press("F6", "ArrowUp", "ArrowUp", "ArrowUp", "ArrowUp")
	d.until("containers view", 5*time.Second, func(p ui.Probe) bool { return p.View == "Containers" })
	d.press("F6", "ArrowDown")
	d.until("web selected", 5*time.Second, func(p ui.Probe) bool { return p.Selected != "" })
	d.until("logs", 30*time.Second, func(p ui.Probe) bool { return strings.Contains(p.Logs, "tick 2") })
	d.until("events and stats", 30*time.Second, func(p ui.Probe) bool {
		return p.Live == "live (GET /events)" && p.Memory > 0 && p.CPU == "--"
	})
	d.click("containers.stop")
	p := d.until("stopped", 30*time.Second, func(p ui.Probe) bool { return strings.Contains(p.Status[1], "0 running") && p.Pending == 0 })
	if p.LastCommand != "wctr stop web" {
		t.Errorf("last command = %q", p.LastCommand)
	}
	// The terminal shows what the button did.
	d.press("F6", "ArrowDown", "ArrowDown", "ArrowDown", "ArrowDown")
	d.until("the action in the terminal", 10*time.Second, func(p ui.Probe) bool {
		return strings.Contains(p.Terminal, "# dashboard: wctr stop web")
	})
	// Networks: one up from the terminal (the navigation has the focus);
	// create one by name.
	d.press("ArrowUp")
	p = d.until("networks view", 10*time.Second, func(p ui.Probe) bool { return p.View == "Networks" && len(p.Networks) > 0 })
	if _, ok := p.Boxes["class.button"]; ok {
		t.Error("this page cannot join a class, yet the dashboard offers it")
	}
	d.click("networks.name")
	d.typeKeys("lab" + fmt.Sprint(time.Now().UnixNano()%1000))
	d.click("networks.create")
	d.until("network created", 30*time.Second, func(p ui.Probe) bool {
		return strings.HasPrefix(p.LastCommand, "wctr network create lab") && len(p.Networks) > 1 && p.Pending == 0
	})
}

// typeKeys presses one key per character, as a person types.
func (d *driver) typeKeys(s string) {
	d.t.Helper()
	for _, c := range s {
		k := string(c)
		if c == ' ' {
			k = "Space"
		}
		d.press(k)
	}
}

// classStub is a page that can join a class, standing in for the
// playground's: window.__wctrClass with a relay already chosen, and a
// record of what the page posts to the engine worker as {t:"join"}.
const classStub = `(() => {
  window.__joins = [];
  const post = Worker.prototype.postMessage;
  Worker.prototype.postMessage = function (m, ...rest) {
    if (m && m.t === "join") window.__joins.push(JSON.stringify(m));
    return post.call(this, m, ...rest);
  };
  window.__wctrClass = { relay: "https://relay.example", used: [],
    useRelay(o) { this.used.push(o); }, status() { return null; } };
})()`

// TestJoinDialog types a join code into the dashboard's dialog in a real
// browser and checks it went to the engine worker once and nowhere else:
// not the URL, the storages, the DOM, the test hook, the screen's text.
func TestJoinDialog(t *testing.T) {
	if os.Getenv("E2E_URL") != "" {
		t.Skip("the deployed page is the playground's to test")
	}
	if only := os.Getenv("E2E_BROWSERS"); only != "" && !strings.Contains(only, "chromium") {
		t.Skip("Chromium only")
	}
	pw, err := playwright.Run()
	if err != nil {
		t.Fatalf("playwright: %v", err)
	}
	defer pw.Stop()
	b, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true), Timeout: playwright.Float(180000)})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	page, err := b.NewPage(playwright.BrowserNewPageOptions{Viewport: &playwright.Size{Width: 1280, Height: 800}})
	if err != nil {
		t.Fatal(err)
	}
	if err := page.AddInitScript(playwright.Script{Content: playwright.String(classStub)}); err != nil {
		t.Fatal(err)
	}
	var log []string
	page.On("console", func(m playwright.ConsoleMessage) { log = append(log, m.Text()) })
	if _, err := page.Goto(serve(t, true)); err != nil {
		t.Fatal(err)
	}
	d := &driver{t: t, page: page, log: &log}
	d.until("the class offer", 90*time.Second, func(p ui.Probe) bool {
		return p.Boxes["class.button"].W > 0 || p.View == "Containers" && strings.HasPrefix(p.Status[0], "engine ")
	})
	d.press("Alt+4")
	d.until("class button", 10*time.Second, func(p ui.Probe) bool { return p.View == "Networks" && p.Boxes["class.button"].W > 0 })
	d.click("class.button")
	d.until("dialog", 5*time.Second, func(p ui.Probe) bool { return p.ClassOpen && p.Focus == "class.name" })
	d.typeKeys("ada")
	d.click("class.code")
	const code = "S3CRET-JOIN-CODE"
	d.typeKeys(code)
	d.until("code typed", 5*time.Second, func(p ui.Probe) bool { return p.CodeLength == len(code) })
	d.press("Enter")
	p := d.until("code sent", 10*time.Second, func(p ui.Probe) bool { return p.CodeLength == 0 })
	joins, err := page.Evaluate("() => window.__joins")
	if err != nil {
		t.Fatal(err)
	}
	if js, _ := joins.([]any); len(js) != 1 || js[0] != `{"t":"join","code":"`+code+`","name":"ada"}` {
		t.Errorf("joins posted = %v", joins)
	}
	where, err := page.Evaluate(`() => [location.href, JSON.stringify(localStorage), JSON.stringify(sessionStorage),
		document.documentElement.outerHTML, window.__dashboard.state()].join("\n")`)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := where.(string)
	// The planted positive: the name, which is not secret, IS in what was
	// read (the probe's last command), so a code there would be seen too.
	if !strings.Contains(s, "join class at https://relay.example as ada") {
		t.Fatalf("the leak check read nothing useful (%d bytes)", len(s))
	}
	if strings.Contains(s, code) {
		t.Error("the join code is readable outside the worker")
	}
	if !strings.HasPrefix(p.LastCommand, "join class at https://relay.example as ada") {
		t.Errorf("last command = %q", p.LastCommand)
	}
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
		if os.Getenv("E2E_URL") != "" {
			if !c.isolate && c.browser == "chromium" {
				continue // the live page is one page: each browser once
			}
			name = c.browser + "/live"
		}
		if only := os.Getenv("E2E_BROWSERS"); only != "" && !strings.Contains(only, c.browser) {
			continue
		}
		t.Run(name, func(t *testing.T) { session(t, pw, c.browser, c.isolate) })
	}
}
