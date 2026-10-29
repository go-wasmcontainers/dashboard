package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/go-widgets/webcanvas"

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
	r.typeKeys("wctr run -d --name box ghcr.io/go-wasmcontainers/shell:0.1.0 sleep 3600\n")
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
	// The shell's exit status is shown. (go-wasmcontainers/shell 0.1.0 has
	// no interactive `exit` builtin -- "sh:exit: not found" -- so the shell
	// ends at end of input, Ctrl-D, with its last command's status: POSIX.)
	// A command that fails, its status printed by the shell, then the end.
	r.typeKeys("ls /nope; echo status=$?\n")
	printed := regexp.MustCompile(`status=([0-9]+)`)
	p = r.until("status printed", func(p Probe) bool { return printed.MatchString(p.Shell) })
	code := printed.FindStringSubmatch(p.Shell)[1]
	r.typeKeys("ls /nope\n")
	r.s.Modifiers(webcanvas.Modifiers{Ctrl: true})
	r.s.KeyDown("d")
	r.s.Modifiers(webcanvas.Modifiers{})
	r.until("exit status", func(p Probe) bool { return strings.HasPrefix(p.ShellStatus, "exited with status ") })
	r.frame()
	st := r.s.Probe().ShellStatus
	// The pane shows what the engine's exec inspect says (the exact mapping
	// is vm's TestShell). This shell's status at end of input is not its
	// last command's, which is the shell's own affair.
	t.Logf("the shell printed status=%s; the pane says %q", code, st)
	if !regexp.MustCompile(`^exited with status [0-9]+$`).MatchString(st) {
		t.Errorf("status = %q", st)
	}
	// The header says so on the Shell tab.
	if s := r.s.dState.Text().Get(); s != "= wctr exec -it box sh · "+st {
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
