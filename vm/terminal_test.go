package vm

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/go-wasmcontainers/engine/api"
	"github.com/go-wasmcontainers/engine/cli"
	"github.com/go-wasmcontainers/engine/cli/lineedit"
	"github.com/go-widgets/toolkit/vt"
	"github.com/spf13/cobra"
)

// termRig is a terminal attached to a vt screen.
type termRig struct {
	*harness
	s      *vt.Screen
	clears int
}

func newTermRig(t *testing.T) *termRig {
	r := &termRig{harness: newHarness(t), s: vt.New(80, 12, 200)}
	return r
}

func (r *termRig) attach() {
	r.app.Terminal.Attach(func(out string) { r.s.WriteString(out) }, func() {
		r.clears++
		r.s.WriteString("\x1b[H\x1b[2J")
	})
}

func (r *termRig) text() string { return r.s.Text(vt.Pos{}, vt.Pos{Line: r.s.Lines(), Col: 80}) }

func (r *termRig) line() string {
	_, y, _ := r.s.Cursor()
	l := r.s.ScrollbackLen() + y
	return r.s.Text(vt.Pos{Line: l}, vt.Pos{Line: l, Col: 79})
}

// typeWait types in and waits until the screen shows want.
func (r *termRig) typeWait(in, want string) {
	r.t.Helper()
	r.app.Terminal.Input(in)
	r.until(want, func() bool { return strings.Contains(r.text(), want) })
}

func TestTerminal(t *testing.T) {
	r := newTermRig(t)
	term := r.app.Terminal
	term.note("before attach: not shown")
	term.write("early ")
	r.attach()
	r.attach() // greets once
	if !strings.HasPrefix(r.text(), "early wctr in the dashboard") || strings.Count(r.text(), "in the dashboard") != 1 {
		t.Fatalf("greeting = %q", r.text())
	}
	r.typeWait("wctr ps\r", "no shell attached")

	sh := &recShell{}
	term.SetShell(sh, lineedit.WctrCompleter(cli.Options{}, Builtins))
	term.Resize(80)
	r.typeWait("wctr images\r", "ran wctr images")
	r.until("prompt back", func() bool { return !term.Running.Get() && r.line() == "$" })
	// History survives SetShell; Up recalls.
	term.SetShell(sh, nil)
	r.typeWait("\x1b[A", "$ wctr images")
	r.typeWait("\x03", "^C")
	r.until("prompt after ^C", func() bool { return r.line() == "$" })
	sh.code = 3
	r.typeWait("wctr ps\r", "[exit status 3]")
	sh.code, sh.clear = 0, true
	r.app.Terminal.Input("clear\r")
	r.until("cleared", func() bool { return r.clears == 1 && !term.Running.Get() })
	sh.clear = false
	r.app.Terminal.Input("\x0c")
	r.until("Ctrl-L", func() bool { return r.clears == 2 })
	r.typeWait("   \r", "$")
	// Completion lists candidates above the prompt.
	term.SetShell(sh, lineedit.WctrCompleter(cli.Options{}, Builtins))
	r.typeWait("\t\t", "clear")
	r.typeWait("\x15", "$")

	// A running command gets Ctrl-C only.
	sh.block = make(chan struct{})
	r.app.Terminal.Input("wctr logs -f clock\r")
	r.until("running", func() bool { return term.Running.Get() })
	term.note("wctr rm web") // not while a command runs
	r.app.Terminal.Input("xyz\x03")
	r.until("interrupted", func() bool { return !term.Running.Get() })
	r.settle()
	if strings.Contains(r.text(), "xyz") || strings.Contains(r.text(), "dashboard: wctr rm web") {
		t.Errorf("keys reached a running command:\n%s", r.text())
	}
	sh.block = nil

	// Dashboard actions show above the prompt, cleaned.
	term.note("wctr rm \x1b[31mweb")
	r.until("note", func() bool { return strings.Contains(r.text(), "# dashboard: wctr rm �[31mweb") })
	term.ShowActions.Set(false)
	term.note("wctr rm hidden")
	if strings.Contains(r.text(), "hidden") {
		t.Error("note shown while off")
	}

	// A full input queue drops keys instead of blocking.
	t2 := newTerminal(r.app)
	for i := 0; i < inputQueue+5; i++ {
		t2.Input("x")
	}
	if len(t2.in) != inputQueue {
		t.Errorf("queue = %d", len(t2.in))
	}
	for i := 0; i < 300; i++ {
		t2.write("x")
	}
	if len(t2.pending) != 256 {
		t.Errorf("pending = %d", len(t2.pending))
	}
	t2.Resize(100) // before an editor exists
	// Without the view's Clear, the escape sequences clear.
	var b strings.Builder
	t2.Out = func(s string) { b.WriteString(s) }
	t2.clear()
	if b.String() != "\x1b[H\x1b[2J" {
		t.Errorf("clear = %q", b.String())
	}
}

func TestWctrShell(t *testing.T) {
	sh := WctrShell{Root: func() *cobra.Command {
		return cli.RootCmd(cli.Options{Version: "t", Client: func(string) (*api.Client, error) {
			return api.NewClientWith(&http.Client{Transport: failRT{}}, "http://engine"), nil
		}})
	}}
	run := func(line string) (string, int, bool) {
		var b bytes.Buffer
		code, clear := sh.Run(context.Background(), line, &b)
		return b.String(), code, clear
	}
	if out, code, _ := run("wctr --help"); code != 0 || !strings.Contains(out, "Usage:") {
		t.Errorf("help: %d %q", code, out)
	}
	if out, code, _ := run("wctr ps"); code == 0 || !strings.Contains(out, "cannot connect") {
		t.Errorf("ps: %d %q", code, out)
	}
	if _, code, clear := run("clear"); code != 0 || !clear {
		t.Error("clear")
	}
	if out, _, _ := run("help"); !strings.Contains(out, "wctr") {
		t.Error("help")
	}
	if out, code, _ := run("ls -l"); code != 127 || !strings.Contains(out, "ls: command not found") {
		t.Errorf("ls: %d %q", code, out)
	}
	if _, code, _ := run(`wctr "x`); code != 2 {
		t.Error("syntax error")
	}
	if _, code, _ := run("  "); code != 0 {
		t.Error("empty")
	}
}

type failRT struct{}

func (failRT) RoundTrip(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded }
