//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"

	"github.com/go-wasmcontainers/dashboard/ui"
	"github.com/go-wasmcontainers/dashboard/vm"
)

// TestSurfaceMouseLock drives the mouse on a surface pane in Chromium: every
// button reaches the guest; once the guest asks for the pointer lock
// ({t:"surface", op:"capture"}) the next click locks the pointer without being
// the guest's, relative motion is sent instead of absolute moves, and the
// release (Escape) brings the absolute moves back.
func TestSurfaceMouseLock(t *testing.T) {
	if os.Getenv("E2E_URL") != "" {
		t.Skip("synthetic frames: a local page only")
	}
	if only := os.Getenv("E2E_BROWSERS"); only != "" && !strings.Contains(only, "chromium") {
		t.Skip("Chromium only")
	}
	pw, err := playwright.Run()
	if err != nil {
		t.Fatalf("playwright: %v", err)
	}
	defer pw.Stop()
	b, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true), Timeout: playwright.Float(180000),
		// The new headless mode: the old headless shell has no pointer lock.
		Channel: playwright.String("chromium")})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	page, err := b.NewPage(playwright.BrowserNewPageOptions{Viewport: &playwright.Size{Width: 1280, Height: 800}})
	if err != nil {
		t.Fatal(err)
	}
	if err := page.AddInitScript(playwright.Script{Content: playwright.String(surfaceStub)}); err != nil {
		t.Fatal(err)
	}
	var log []string
	page.On("console", func(m playwright.ConsoleMessage) { log = append(log, m.Text()) })
	if _, err := page.Goto(serve(t, true)); err != nil {
		t.Fatal(err)
	}
	d := &driver{t: t, page: page, log: &log}
	d.until("the dashboard", 90*time.Second, func(p ui.Probe) bool { return strings.HasPrefix(p.Status[0], "engine ") })
	d.goTo("Surfaces")
	d.until("surfaces view", 10*time.Second, func(p ui.Probe) bool { return p.View == "Surfaces" })
	if _, err := page.Evaluate(emit, []any{3, 0, 255, 0}); err != nil {
		t.Fatal(err)
	}
	p := d.until("a pane", 20*time.Second, func(p ui.Probe) bool { return len(p.SurfaceBoxes) == 1 && p.SurfaceBoxes[0].W > 0 })
	box := p.SurfaceBoxes[0]
	cx, cy := box.X+box.W/2, box.Y+box.H/2
	img := vm.Box{X: box.X, Y: box.Y, W: box.W, H: box.H}
	sx, sy, _ := vm.ToSurface(img, 64, 48, cx, cy)
	at := fmt.Sprintf("%.2f,%.2f", sx, sy)

	inputs := func() []string {
		v, err := page.Evaluate(`() => window.__inputs`)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, a := range v.([]any) {
			out = append(out, a.(string))
		}
		return out
	}
	locked := func() bool {
		v, err := page.Evaluate(`() => document.pointerLockElement === document.getElementById("screen")`)
		if err != nil {
			t.Fatal(err)
		}
		return v.(bool)
	}
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		d.until(what, 10*time.Second, func(ui.Probe) bool { return cond() })
	}
	has := func(want string) bool {
		for _, s := range inputs() {
			if s == want {
				return true
			}
		}
		return false
	}
	mouse := page.Mouse()

	// The guest did not ask for the lock: a click is the guest's, as pointer
	// and as button, and nothing is locked.
	if err := mouse.Click(float64(cx), float64(cy)); err != nil {
		t.Fatal(err)
	}
	waitFor("a plain click", func() bool { return has("7 button-up 0 " + at) })
	for _, want := range []string{"7 pointer-down " + at, "7 button-down 0 " + at, "7 pointer-up " + at} {
		if !has(want) {
			t.Errorf("a plain click: no %q in %q", want, inputs())
		}
	}
	if locked() {
		t.Fatal("the pointer is locked though the guest did not ask")
	}

	// The guest asks. The next click locks the pointer and is not the guest's.
	if _, err := page.Evaluate(`() => window.__wctrEngine.dispatchEvent(new MessageEvent("message", { data: { t: "surface", op: "capture", surface: 7, capture: true } }))`); err != nil {
		t.Fatal(err)
	}
	// The wish crosses the mvvm queue to the pane: it is there once a frame
	// has been drawn after it.
	time.Sleep(500 * time.Millisecond)
	if _, err := page.Evaluate(`() => document.addEventListener("pointerlockerror", () => { window.__lockError = true })`); err != nil {
		t.Fatal(err)
	}
	// Moves aside (the click's own approach is a move): presses and releases.
	presses := func() (out []string) {
		for _, s := range inputs() {
			if !strings.Contains(s, "pointer-move") {
				out = append(out, s)
			}
		}
		return out
	}
	before := len(presses())
	if err := mouse.Click(float64(cx), float64(cy)); err != nil {
		t.Fatal(err)
	}
	waitFor("the pointer lock", func() bool {
		if e, _ := page.Evaluate(`() => window.__lockError === true`); e == true {
			t.Fatal("the browser refused the pointer lock (pointerlockerror)")
		}
		return locked()
	})
	time.Sleep(300 * time.Millisecond) // a release of the grabbing click would arrive by now
	if got := presses(); len(got) != before {
		t.Errorf("the click that locked the pointer reached the guest: %q", got[before:])
	}

	// Locked: a relative motion is sent (scaled from canvas to surface
	// pixels), a right press is button 2, and a plain move is not sent as a
	// position. The browser reports movementX/Y itself; the test's events
	// carry them (a trusted mouse move in a headless page does not).
	if _, err := page.Evaluate(`() => document.getElementById("screen").dispatchEvent(new MouseEvent("mousemove", { movementX: 20, movementY: -10, bubbles: true }))`); err != nil {
		t.Fatal(err)
	}
	wantMotion := fmt.Sprintf("7 pointer-motion %.2f,%.2f", 20*64/float64(box.W), -10*48/float64(box.H))
	waitFor("the motion", func() bool { return has(wantMotion) })
	if err := mouse.Down(playwright.MouseDownOptions{Button: playwright.MouseButtonRight}); err != nil {
		t.Fatal(err)
	}
	if err := mouse.Up(playwright.MouseUpOptions{Button: playwright.MouseButtonRight}); err != nil {
		t.Fatal(err)
	}
	waitFor("the right button", func() bool { return has("7 button-down 2 "+at) && has("7 button-up 2 "+at) })

	// Escape releases the pointer; absolute moves are the guest's again.
	if err := page.Keyboard().Press("Escape"); err != nil {
		t.Fatal(err)
	}
	if locked() {
		// A headless browser may leave Escape to its own UI: release as the
		// browser does, so the rest of the page's behaviour is still checked.
		t.Log("Escape did not release the lock in this headless page: document.exitPointerLock()")
		if _, err := page.Evaluate(`() => document.exitPointerLock()`); err != nil {
			t.Fatal(err)
		}
	}
	waitFor("the release", func() bool { return !locked() })
	if err := mouse.Move(float64(cx+3), float64(cy+3)); err != nil {
		t.Fatal(err)
	}
	waitFor("an absolute move again", func() bool {
		for _, s := range inputs() {
			if strings.HasPrefix(s, "7 pointer-move ") {
				return true
			}
		}
		return false
	})
}
