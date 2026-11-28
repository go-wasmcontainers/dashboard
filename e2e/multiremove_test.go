//go:build e2e

package e2e

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"

	"github.com/go-wasmcontainers/dashboard/ui"
)

// command types one terminal line and runs it.
func (d *driver) command(line string) {
	d.t.Helper()
	// Keys typed before the previous command's prompt are lost: wait for it.
	atPrompt := func(p ui.Probe) bool {
		return p.Pending == 0 && !p.Running && strings.HasSuffix(strings.TrimRight(p.Terminal, " \n"), "$")
	}
	d.until("a prompt", 60*time.Second, atPrompt)
	d.typeKeys(line)
	d.press("Enter")
	d.until("`"+line+"` done", 60*time.Second, func(p ui.Probe) bool {
		return atPrompt(p) && strings.Contains(p.Terminal, "$ "+line)
	})
}

// selectRows selects the rows of table named in names (in the list rows):
// a click on the first, Shift-clicks on the others (Ctrl-click is the
// secondary click on a Mac), as a person does. The rows are adjacent.
func (d *driver) selectRows(table string, rows, names []string) {
	d.t.Helper()
	for k, name := range names {
		i := slices.Index(rows, name)
		if i < 0 {
			d.t.Fatalf("%s: no row %q in %v", table, name, rows)
		}
		box, ok := d.state().Boxes[table+".row."+strconv.Itoa(i)]
		if !ok {
			d.t.Fatalf("%s: row %d is not on screen", table, i)
		}
		if k > 0 {
			if err := d.page.Keyboard().Down("Shift"); err != nil {
				d.t.Fatal(err)
			}
		}
		if err := d.page.Mouse().Click(float64(box.X+box.W/2), float64(box.Y+box.H/2)); err != nil {
			d.t.Fatal(err)
		}
		if k > 0 {
			if err := d.page.Keyboard().Up("Shift"); err != nil {
				d.t.Fatal(err)
			}
		}
	}
}

// removeSelected hovers Remove (its line must name every command), clicks
// it, and waits for the engine to be done.
func (d *driver) removeSelected(button string, count int) {
	d.t.Helper()
	b, ok := d.state().Boxes[button]
	if !ok {
		d.t.Fatalf("no %s on screen", button)
	}
	if err := d.page.Mouse().Move(float64(b.X+b.W/2), float64(b.Y+b.H/2)); err != nil {
		d.t.Fatal(err)
	}
	d.until("the hover line", 5*time.Second, func(p ui.Probe) bool {
		return strings.HasPrefix(p.Hint, "Remove = ") && strings.Contains(p.Hint, "commands")
	})
	d.click(button)
	p := d.until("the removal done", 30*time.Second, func(p ui.Probe) bool { return p.Pending == 0 && strings.HasPrefix(p.LastCommand, "3 commands: ") })
	if p.Error != "" {
		d.t.Errorf("error after removing %d rows: %s", count, p.Error)
	}
}

// TestMultiRemove selects three of four rows in each table and presses
// Remove: the three are gone, the fourth stays.
func TestMultiRemove(t *testing.T) {
	if os.Getenv("E2E_URL") != "" {
		t.Skip("it creates and removes objects: a local build only")
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
	var log []string
	page.On("console", func(m playwright.ConsoleMessage) { log = append(log, m.Text()) })
	page.On("pageerror", func(err error) { log = append(log, "pageerror: "+err.Error()) })
	if _, err := page.Goto(serve(t, true)); err != nil {
		t.Fatal(err)
	}
	d := &driver{t: t, page: page, log: &log}
	d.until("the dashboard", 90*time.Second, func(p ui.Probe) bool {
		return strings.HasPrefix(p.Status[0], "engine ") && !strings.HasPrefix(p.Status[0], "engine: ")
	})

	// Four of each, made from the terminal.
	d.goTo("Terminal")
	d.until("terminal view", 5*time.Second, func(p ui.Probe) bool { return p.View == "Terminal" })
	d.click("terminal")
	for _, n := range []string{"k1", "k2", "k3", "k4"} {
		d.command("wctr run -d --name " + n + " hello")
	}
	d.command("wctr pull clock")
	d.command("wctr pull fail")
	d.command("wctr commit k4 extra:1") // a fourth image, from a container
	for _, n := range []string{"v1", "v2", "v3", "v4"} {
		d.command("wctr volume create " + n)
	}
	for _, n := range []string{"n1", "n2", "n3", "n4"} {
		d.command("wctr network create " + n)
	}

	// Containers.
	d.press("F6") // the terminal keeps the keys: F6 leaves it
	d.goTo("Containers")
	p := d.until("four containers", 30*time.Second, func(p ui.Probe) bool {
		return p.View == "Containers" && len(p.Containers) == 4 && p.Boxes["containers.row.3"].H > 0
	})
	keep := p.Containers[3]
	gone := p.Containers[:3]
	d.selectRows("containers", p.Containers, gone)
	d.removeSelected("containers.remove", 3)
	p = d.until("containers removed", 30*time.Second, func(p ui.Probe) bool { return len(p.Containers) <= 1 })
	if !slices.Equal(p.Containers, []string{keep}) {
		t.Errorf("containers after Remove = %v, want [%s]", p.Containers, keep)
	}

	// Images: clock, extra and fail go; hello (the containers' image) stays.
	d.goTo("Images")
	p = d.until("four images", 30*time.Second, func(p ui.Probe) bool {
		return p.View == "Images" && len(p.Images) == 4 && p.Boxes["images.row.3"].H > 0
	})
	d.selectRows("images", p.Images, []string{"clock:latest", "extra:1", "fail:latest"})
	d.removeSelected("images.remove", 3)
	p = d.until("images removed", 30*time.Second, func(p ui.Probe) bool { return len(p.Images) <= 1 })
	if !slices.Equal(p.Images, []string{"hello:latest"}) {
		t.Errorf("images after Remove = %v, want [hello:latest]", p.Images)
	}

	// Volumes.
	d.goTo("Volumes")
	p = d.until("four volumes", 30*time.Second, func(p ui.Probe) bool {
		return p.View == "Volumes" && len(p.VolumeNames) == 4 && p.Boxes["volumes.row.3"].H > 0
	})
	d.selectRows("volumes", p.VolumeNames, []string{"v1", "v2", "v3"})
	d.removeSelected("volumes.remove", 3)
	p = d.until("volumes removed", 30*time.Second, func(p ui.Probe) bool { return len(p.VolumeNames) <= 1 })
	if !slices.Equal(p.VolumeNames, []string{"v4"}) {
		t.Errorf("volumes after Remove = %v, want [v4]", p.VolumeNames)
	}

	// Networks (the engine's own networks, if any, stay).
	d.goTo("Networks")
	p = d.until("the networks", 30*time.Second, func(p ui.Probe) bool {
		return p.View == "Networks" && slices.Contains(p.Networks, "n4") && p.Boxes["networks.row.0"].H > 0
	})
	d.selectRows("networks", p.Networks, []string{"n1", "n2", "n3"})
	d.removeSelected("networks.remove", 3)
	p = d.until("networks removed", 30*time.Second, func(p ui.Probe) bool { return !slices.Contains(p.Networks, "n1") })
	if slices.Contains(p.Networks, "n2") || slices.Contains(p.Networks, "n3") || !slices.Contains(p.Networks, "n4") {
		t.Errorf("networks after Remove = %v, want n1-n3 gone and n4 kept", p.Networks)
	}
}
