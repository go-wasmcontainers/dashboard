//go:build e2e

package e2e

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"

	"github.com/go-wasmcontainers/dashboard/ui"
)

// insideRepo reports whether dir is inside a git work tree (a crop must
// never land where it could be committed).
func insideRepo(dir string) bool {
	d, _ := filepath.Abs(dir)
	for {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return true
		}
		p := filepath.Dir(d)
		if p == d {
			return false
		}
		d = p
	}
}

// TestTextPixels measures the title text as the canvas holds it, at device
// pixel ratios 1 and 2: the canvas backing store against its CSS size, and
// the number of distinct colours along the glyphs (1-bit text has 2).
// E2E_CROPS=<dir outside any repository> saves the crops as PNG.
func TestTextPixels(t *testing.T) {
	if only := os.Getenv("E2E_BROWSERS"); only != "" && !strings.Contains(only, "chromium") {
		t.Skip("measured in Chromium; E2E_BROWSERS excludes it")
	}
	crops := os.Getenv("E2E_CROPS")
	if crops != "" && insideRepo(crops) {
		t.Fatalf("E2E_CROPS %s is inside a git work tree", crops)
	}
	pw, err := playwright.Run()
	if err != nil {
		t.Fatalf("playwright: %v", err)
	}
	defer pw.Stop()
	b, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true)})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, dpr := range []float64{1, 2} {
		page, err := b.NewPage(playwright.BrowserNewPageOptions{Viewport: &playwright.Size{Width: 1280, Height: 800}, DeviceScaleFactor: playwright.Float(dpr)})
		if err != nil {
			t.Fatal(err)
		}
		var log []string
		if _, err := page.Goto(serve(t, true)); err != nil {
			t.Fatal(err)
		}
		d := &driver{t: t, page: page, log: &log}
		d.until("ready", 90*time.Second, func(p ui.Probe) bool { return len(p.Status) > 0 && p.Status[0] != "" })
		time.Sleep(300 * time.Millisecond)
		grab := func(name string, x, y, w, h int) map[string]any {
			v, err := page.Evaluate(`([x, y, w, h]) => {
				const c = document.getElementById("screen");
				const k = c.width / c.clientWidth;
				[x, y, w, h] = [x, y, w, h].map((n) => Math.round(n * k));
				const px = c.getContext("2d").getImageData(x, y, w, h).data;
				return {dpr: devicePixelRatio, width: c.width, cssWidth: c.clientWidth, w, h, px: Array.from(px)};
			}`, []int{x, y, w, h})
			if err != nil {
				t.Fatal(err)
			}
			m := v.(map[string]any)
			m["name"] = name
			return m
		}
		// The layout: sidebar, its edge, the containers table's frame.
		save(t, crops, grab("layout", 0, 40, 700, 260), dpr)
		m := grab("title", 8, 8, 300, 24)
		levels := save(t, crops, m, dpr)
		t.Logf("dpr %v: canvas %v px for %v CSS px (backing/CSS %.2f); title crop holds %d distinct colours",
			m["dpr"], m["width"], m["cssWidth"], toFloat(m["width"])/toFloat(m["cssWidth"]), levels)
		page.Close()
	}
}

// save writes a grabbed region as PNG under crops (when set) and returns
// how many distinct colours it holds.
func save(t *testing.T, crops string, m map[string]any, dpr float64) int {
	w, h := toInt(m["w"]), toInt(m["h"])
	raw := m["px"].([]any)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	levels := map[color.RGBA]bool{}
	for i := 0; i+3 < len(raw); i += 4 {
		c := color.RGBA{uint8(toInt(raw[i])), uint8(toInt(raw[i+1])), uint8(toInt(raw[i+2])), uint8(toInt(raw[i+3]))}
		levels[c] = true
		img.SetRGBA((i/4)%w, (i/4)/w, c)
	}
	if crops != "" {
		os.MkdirAll(crops, 0o755)
		name := filepath.Join(crops, fmt.Sprintf("%s-%s-dpr%v.png", os.Getenv("E2E_CROP_TAG"), m["name"], dpr))
		if f, err := os.Create(name); err == nil {
			png.Encode(f, img)
			f.Close()
			t.Logf("crop: %s", name)
		}
	}
	return len(levels)
}

func toInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	}
	return 0
}

func toFloat(v any) float64 { return float64(toInt(v)) }
