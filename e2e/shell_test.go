//go:build e2e

package e2e

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"

	"github.com/go-wasmcontainers/dashboard/ui"
)

// TestShell opens a shell inside a container in a real browser, through
// the bridge's streamed request body and the browser executor's exec:
// typed one key at a time, its output read back from the test hook.
func TestShell(t *testing.T) {
	pw, err := playwright.Run()
	if err != nil {
		t.Fatalf("playwright: %v", err)
	}
	defer pw.Stop()
	for _, browser := range []string{"chromium", "firefox"} {
		if only := os.Getenv("E2E_BROWSERS"); only != "" && !strings.Contains(only, browser) {
			continue
		}
		t.Run(browser, func(t *testing.T) { shellSession(t, pw, browser) })
	}
}

func shellSession(t *testing.T, pw *playwright.Playwright, browser string) {
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
	// What the page posts to the engine worker, by message type (a trace
	// for a failure; the stream's http.body messages are the keys).
	if err := page.AddInitScript(playwright.Script{Content: playwright.String(`(() => {
  window.__posted = {};
  const post = Worker.prototype.postMessage;
  Worker.prototype.postMessage = function (m, ...rest) {
    if (m && typeof m.t === "string") window.__posted[m.t + (m.stream ? "+stream" : "")] = (window.__posted[m.t + (m.stream ? "+stream" : "")] || 0) + 1;
    return post.call(this, m, ...rest);
  };
})()`)}); err != nil {
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
	d.press("Alt+5")
	d.until("terminal view", 5*time.Second, func(p ui.Probe) bool { return p.View == "Terminal" })
	d.click("terminal")
	d.typeKeys("wctr run -d --name box ghcr.io/go-wasmcontainers/shell:0.2.0 sleep 3600")
	d.press("Enter")
	d.until("box running", 120*time.Second, func(p ui.Probe) bool {
		return len(p.Containers) == 1 && p.Containers[0] == "box" && strings.Contains(p.Status[1], "1 running")
	})
	// F6 leaves the terminal (which keeps Alt+keys while focused), Alt+1
	// shows the containers, F6 reaches their table, ArrowDown selects box.
	d.press("F6", "Alt+1")
	d.until("containers view", 5*time.Second, func(p ui.Probe) bool { return p.View == "Containers" })
	d.press("F6", "ArrowDown")
	d.until("box selected", 10*time.Second, func(p ui.Probe) bool { return p.Selected != "" })
	d.click("containers.shell")
	p := d.until("the shell", 60*time.Second, func(p ui.Probe) bool { return p.ShellStatus == "running" && strings.Contains(p.Shell, "$") })
	if p.ShellLine != "wctr exec -it box sh" || p.LastCommand != p.ShellLine {
		t.Errorf("line = %q, last command %q", p.ShellLine, p.LastCommand)
	}
	d.typeKeys("ls /bin | wc -l")
	d.press("Enter")
	time.Sleep(2 * time.Second)
	if posted, err := page.Evaluate(`() => JSON.stringify(window.__posted)`); err == nil {
		log = append(log, "posted to the engine worker: "+posted.(string))
	}
	p = d.until("44", 60*time.Second, func(p ui.Probe) bool { return regexp.MustCompile(`(?m)^\s*44\s*$`).MatchString(p.Shell) })
	t.Logf("%s: shell screen:\n%s", browser, strings.TrimSpace(p.Shell))
	// `exit 3` ends the shell; its exit status is shown.
	d.typeKeys("exit 3")
	d.press("Enter")
	p = d.until("exit status", 30*time.Second, func(p ui.Probe) bool { return strings.HasPrefix(p.ShellStatus, "exited with status ") })
	if p.ShellStatus != "exited with status 3" {
		t.Errorf("%s: status = %q, want exited with status 3", browser, p.ShellStatus)
	}
}
