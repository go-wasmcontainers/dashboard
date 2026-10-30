package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/go-wasmcontainers/dashboard/internal/testengine"
	"github.com/go-wasmcontainers/dashboard/vm"
)

// typeKeys types s one key at a time into the scene, as a person does
// (Enter as a key).
func (r *rig) typeKeys(s string) {
	for _, c := range s {
		if c == '\n' {
			r.s.KeyDown("Enter")
			continue
		}
		r.s.Char(string(c))
	}
}

// A shell inside a container, through the real engine and the bridge's
// streamed request body: open it from the toolbar, type into it, read its
// output, and see its exit status.
func TestShellInContainer(t *testing.T) {
	testengine.HasShell(t)
	r := newRig(t)
	r.until("engine", func(p Probe) bool { return strings.HasPrefix(p.Status[0], "engine test") })
	r.app.View.Set(vm.ViewTerminal)
	r.click("terminal")
	r.typeKeys("wctr run -d --name box ghcr.io/go-wasmcontainers/shell:0.2.0 sleep 3600\n")
	// The shell is opened as soon as box is listed as running, before
	// `wctr run -d` has returned: the engine (v0.29.1) makes an exec wait
	// for a started container's process, so there is no window to retry.
	r.until("box running", func(p Probe) bool {
		return len(p.Containers) == 1 && p.Containers[0] == "box" && strings.Contains(p.Status[1], "1 running")
	})
	r.app.View.Set(vm.ViewContainers)
	r.clickRow(r.s.cTable, 0)
	b := r.s.Probe().Boxes["containers.shell"]
	r.s.Move(b.X+3, b.Y+3)
	if h := r.s.Probe().Hint; h != "Open shell = wctr exec -it box sh" {
		t.Errorf("hover = %q", h)
	}
	r.click("containers.shell")
	p := r.until("shell running", func(p Probe) bool { return p.ShellStatus == "running" })
	if p.ShellLine != "wctr exec -it box sh" || p.Focus != "detail" || r.s.detail.Active().Get() != shellTab {
		t.Errorf("open: %q focus %q tab %d", p.ShellLine, p.Focus, r.s.detail.Active().Get())
	}
	r.typeKeys("ls /bin | wc -l\n")
	p = r.until("ls output", func(p Probe) bool { return strings.Contains(p.Shell, "44") })
	if !strings.Contains(p.Shell, "ls /bin | wc -l") {
		t.Errorf("no echo of the line: %q", p.Shell)
	}
	// Arrow keys go to the shell, not to the notebook's tabs.
	r.s.KeyDown("ArrowLeft")
	if r.s.detail.Active().Get() != shellTab {
		t.Error("an arrow key switched the detail's tab")
	}
	// A command typed while the previous one runs is not lost (engine
	// v0.29.2 hands the typed-ahead keys over), and `exit 3` ends the shell
	// with status 3, which the pane shows.
	r.typeKeys("ls /nope; echo status=$?\n")
	r.until("status printed", func(p Probe) bool { return regexp.MustCompile(`status=[1-9][0-9]*`).MatchString(p.Shell) })
	r.typeKeys("exit 3\n")
	r.until("exit status", func(p Probe) bool { return strings.HasPrefix(p.ShellStatus, "exited with status ") })
	r.frame()
	if st := r.s.Probe().ShellStatus; st != "exited with status 3" {
		t.Errorf("status = %q, want exited with status 3\nscreen:\n%s", st, r.s.Probe().Shell)
	}
	// The header says so on the Shell tab.
	if s := r.s.dState.Text().Get(); s != "= wctr exec -it box sh · exited with status 3" {
		t.Errorf("header = %q", s)
	}
}

// A container without /bin/sh: the engine's error is shown.
func TestShellWithoutSh(t *testing.T) {
	r := newRig(t)
	r.until("engine", func(p Probe) bool { return strings.HasPrefix(p.Status[0], "engine test") })
	r.app.View.Set(vm.ViewTerminal)
	r.click("terminal")
	r.typeKeys("wctr run -d --name web clock -interval 20ms\n")
	r.until("web running", func(p Probe) bool { return strings.Contains(p.Status[1], "1 running") })
	r.app.View.Set(vm.ViewContainers)
	r.clickRow(r.s.cTable, 0)
	r.click("containers.shell")
	p := r.until("refused", func(p Probe) bool { return strings.HasPrefix(p.ShellStatus, "exec: ") })
	t.Logf("the engine says: %s", p.ShellStatus)
}
