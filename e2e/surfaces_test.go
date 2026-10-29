//go:build e2e

package e2e

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"

	"github.com/go-wasmcontainers/dashboard/ui"
)

// surfaceStub records what the page posts to the engine worker as
// {t:"frame-ack"}, and every ImageBitmap the test makes.
const surfaceStub = `(() => {
  window.__acks = [];
  window.__bitmaps = [];
  const post = Worker.prototype.postMessage;
  Worker.prototype.postMessage = function (m, ...rest) {
    if (m && m.t === "frame-ack") window.__acks.push(m.surface + ":" + m.seq);
    return post.call(this, m, ...rest);
  };
})()`

// emit dispatches, on the engine worker, the messages the GPU engine posts:
// a surface opened, then n frames of one colour in a single task (so most
// are replaced before a scene frame takes them), and a malformed frame.
const emit = `async ([n, r, g, b]) => {
  const w = window.__wctrEngine;
  const send = (data) => w.dispatchEvent(new MessageEvent("message", { data }));
  send({ t: "surface", op: "open", surface: 7, container: "", pid: 3, width: 64, height: 48 });
  const px = new Uint8ClampedArray(64 * 48 * 4);
  for (let i = 0; i < px.length; i += 4) { px[i] = r; px[i + 1] = g; px[i + 2] = b; px[i + 3] = 255; }
  const frames = [];
  for (let i = 0; i < n; i++) frames.push(await createImageBitmap(new ImageData(px, 64, 48)));
  window.__bitmaps.push(...frames);
  const base = window.__seq || 0;
  frames.forEach((bitmap, i) => send({ t: "frame", surface: 7, seq: base + i + 1, width: 64, height: 48, bitmap }));
  window.__seq = base + n;
  send({ t: "frame", surface: 7, seq: 999999, width: 64, height: 48, bitmap: "not a bitmap" });
}`

// pixelAt reads the scene canvas at (x, y).
const pixelAt = `([x, y]) => Array.from(document.getElementById("screen").getContext("2d").getImageData(x, y, 1, 1).data)`

// TestSurfaceFrames drives the surface pane in Chromium with synthetic
// ImageBitmaps: drawn scaled into the pane, every one acknowledged once and
// closed, the last frame greyed when the surface closes.
func TestSurfaceFrames(t *testing.T) {
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
	b, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true), Timeout: playwright.Float(180000)})
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
	d.press("Alt+6")
	d.until("surfaces view", 10*time.Second, func(p ui.Probe) bool { return p.View == "Surfaces" })

	const n = 30
	if _, err := page.Evaluate(emit, []any{n, 255, 0, 0}); err != nil {
		t.Fatal(err)
	}
	acks := func() []string {
		v, err := page.Evaluate(`() => window.__acks`)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, a := range v.([]any) {
			out = append(out, a.(string))
		}
		return out
	}
	p := d.until("every frame acknowledged", 20*time.Second, func(p ui.Probe) bool {
		return len(acks()) == n+1 && len(p.SurfaceBoxes) == 1 && p.SurfaceBoxes[0].W > 0
	})
	if !strings.HasPrefix(p.Surfaces[0], "pid 3 — surface 7 | 64x48, ") {
		t.Errorf("pane = %q", p.Surfaces[0])
	}
	seen := map[string]int{}
	for _, a := range acks() {
		seen[a]++
	}
	for i := 1; i <= n; i++ {
		if k := seen["7:"+itoa(i)]; k != 1 {
			t.Errorf("frame %d acknowledged %d times", i, k)
		}
	}
	if seen["7:999999"] != 1 {
		t.Error("the malformed frame was not acknowledged (the guest would stall)")
	}
	// Drawn: the centre of the frame's box is the frames' red, and it keeps
	// the 4:3 aspect.
	box := p.SurfaceBoxes[0]
	if box.W*48 < box.H*64-64 || box.W*48 > box.H*64+64 {
		t.Errorf("frame box %+v is not 4:3", box)
	}
	px, err := page.Evaluate(pixelAt, []int{box.X + box.W/2, box.Y + box.H/2})
	if err != nil {
		t.Fatal(err)
	}
	if c := px.([]any); c[0].(int) < 240 || c[1].(int) > 15 || c[2].(int) > 15 {
		t.Errorf("frame centre = %v, not red", c)
	}
	// The leak check: every bitmap made is closed (a closed ImageBitmap's
	// width is 0, HTML's ImageBitmap close()).
	open, err := page.Evaluate(`() => window.__bitmaps.filter(b => b.width !== 0).length`)
	if err != nil {
		t.Fatal(err)
	}
	if open.(int) != 0 {
		t.Errorf("%v of %d ImageBitmaps were never closed", open, n)
	}

	// Close: the last frame stays, greyed, and a Dismiss appears.
	if _, err := page.Evaluate(`() => window.__wctrEngine.dispatchEvent(new MessageEvent("message", { data: { t: "surface", op: "close", surface: 7 } }))`); err != nil {
		t.Fatal(err)
	}
	p = d.until("closed", 10*time.Second, func(p ui.Probe) bool {
		return len(p.Surfaces) == 1 && strings.HasSuffix(p.Surfaces[0], "closed") && p.Boxes["surface.7.dismiss"].W > 0
	})
	px, err = page.Evaluate(pixelAt, []int{box.X + box.W/2, box.Y + box.H/2})
	if err != nil {
		t.Fatal(err)
	}
	if c := px.([]any); c[0].(int) > 160 || c[0].(int) < 60 {
		t.Errorf("closed frame centre = %v, not greyed", c)
	}
	d.click("surface.7.dismiss")
	d.until("dismissed", 10*time.Second, func(p ui.Probe) bool { return len(p.Surfaces) == 0 })
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for ; i > 0; i /= 10 {
		s = string(rune('0'+i%10)) + s
	}
	return s
}
