package vm

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-wasmcontainers/engine/cli"
	"github.com/go-wasmcontainers/engine/cli/lineedit"
	"github.com/go-widgets/mvvm"
	"github.com/spf13/cobra"
)

// Shell runs one command line, writing its output to out, and returns the
// exit status and whether the screen should be cleared. The playground's
// session has this shape; WctrShell is the dashboard's own.
type Shell interface {
	Run(ctx context.Context, line string, out io.Writer) (code int, clear bool)
}

// WctrShell runs `wctr …` lines through wctr's command tree in-process.
type WctrShell struct {
	// Root returns a fresh wctr command tree (cli.RootCmd with a Client
	// that speaks to the engine).
	Root func() *cobra.Command
}

// Builtins are the shell's own words, for completion.
var Builtins = []cli.Candidate{
	{Value: "wctr", Description: "the container CLI"},
	{Value: "help", Description: "what this terminal does"},
	{Value: "clear", Description: "clear the screen"},
}

// Run implements Shell.
func (s WctrShell) Run(ctx context.Context, line string, out io.Writer) (int, bool) {
	words, err := lineedit.Split(line)
	if err != nil {
		fmt.Fprintln(out, "syntax error:", err)
		return 2, false
	}
	if len(words) == 0 {
		return 0, false
	}
	switch words[0] {
	case "clear":
		return 0, true
	case "help":
		fmt.Fprintln(out, "This terminal runs wctr, the container CLI, against the same engine as the dashboard.")
		fmt.Fprintln(out, "Try: wctr images · wctr run hello · wctr ps -a · wctr logs NAME · wctr rm NAME · wctr --help")
		fmt.Fprintln(out, "Keys: Tab completes, Up/Down and Ctrl-R history, Ctrl-C interrupts, Ctrl-L clears.")
		return 0, false
	case "wctr":
	default:
		fmt.Fprintf(out, "%s: command not found (this terminal runs wctr; type help)\n", Clean(words[0], 64))
		return 127, false
	}
	cmd := s.Root()
	cmd.SetArgs(words[1:])
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetIn(strings.NewReader(""))
	if err := cmd.ExecuteContext(ctx); err != nil {
		return cli.Report(out, err), false
	}
	return 0, false
}

// Terminal is the terminal view's model: the engine's line editor
// (cli/lineedit, the one the playground uses) in front of a Shell. Input
// arrives as the bytes a terminal sends; output goes to Out (the view's
// terminal widget, which is safe for untrusted output by construction).
//
// Keys are handled on the terminal's own goroutine, never the UI's: Tab
// completion asks the engine (container names) and must not block the
// page's event loop, which is what delivers the engine's answer.
type Terminal struct {
	app      *App
	shell    Shell
	complete lineedit.Completer

	// Out receives everything to draw and Clear clears the screen; both
	// are set by Attach and called on the UI goroutine.
	Out   func(string)
	Clear func()
	// ShowActions echoes each dashboard action's command line here.
	ShowActions *mvvm.Observable[bool]
	// Running says a command runs (only Ctrl-C reaches it).
	Running *mvvm.Observable[bool]

	mu      sync.Mutex
	ed      *lineedit.Editor
	r       *lineedit.Renderer
	run     context.CancelFunc
	in      chan string
	started bool
	pending []string
}

// inputQueue bounds the keystrokes waiting for the terminal's goroutine.
const inputQueue = 256

func newTerminal(a *App) *Terminal {
	return &Terminal{app: a, ShowActions: mvvm.NewObservable(true), Running: mvvm.NewObservable(false),
		in: make(chan string, inputQueue), r: &lineedit.Renderer{Cols: 80}}
}

// SetShell sets what runs the lines and what completes them (nil: no
// completion).
func (t *Terminal) SetShell(s Shell, complete lineedit.Completer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.shell, t.complete = s, complete
	hist := []string(nil)
	if t.ed != nil {
		hist = t.ed.History()
	}
	t.ed = lineedit.New(lineedit.Options{Complete: complete, Cols: t.r.Cols, HistorySize: 500})
	t.ed.SetHistory(hist)
}

// out hands s to the view, from any goroutine.
func (t *Terminal) out(s string) { t.app.post(func() { t.write(s) }) }

// write runs on the UI goroutine.
func (t *Terminal) write(s string) {
	if t.Out == nil {
		if len(t.pending) < 256 {
			t.pending = append(t.pending, s)
		}
		return
	}
	t.Out(s)
}

// Attach connects the view and starts the terminal: greeting, prompt, and
// the goroutine that handles keys. Called on the UI goroutine.
func (t *Terminal) Attach(out func(string), clear func()) {
	t.Out, t.Clear = out, clear
	for _, s := range t.pending {
		out(s)
	}
	t.pending = nil
	if t.started {
		return
	}
	t.started = true
	if t.ed == nil {
		t.SetShell(nil, nil)
	}
	t.mu.Lock()
	t.write("\x1b[1mwctr\x1b[0m in the dashboard: WebAssembly containers, same engine as the views. Type \x1b[1mhelp\x1b[0m; Tab completes.\r\n")
	t.write(t.r.Render(t.ed.State()))
	t.mu.Unlock()
	go t.loop()
}

// Resize tells the editor the terminal's width.
func (t *Terminal) Resize(cols int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.r.Cols = cols
	if t.ed != nil {
		t.ed.SetCols(cols)
	}
}

// Input queues what the user typed. It never blocks (keys past a full
// queue are dropped).
func (t *Terminal) Input(data string) {
	select {
	case t.in <- data:
	default:
	}
}

func (t *Terminal) loop() {
	for data := range t.in {
		for _, k := range lineedit.ParseKeys(data) {
			t.key(k)
		}
	}
}

// note shows a dashboard action's command line above the prompt. Called
// on the UI goroutine.
func (t *Terminal) note(line string) {
	if !t.ShowActions.Get() || !t.started {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.run != nil {
		return // a command owns the screen
	}
	t.write(t.r.Print([]string{"\x1b[2m# dashboard: " + Clean(line, 500) + "\x1b[0m"}, t.ed.State()))
}

func (t *Terminal) key(k lineedit.Key) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.run != nil {
		if k.Code == lineedit.KeyCtrlC {
			t.run()
			t.out("^C")
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	before := t.ed.State()
	ev := t.ed.Key(ctx, k)
	cancel()
	switch {
	case ev.Submit:
		s := lineedit.State{Prompt: t.ed.State().Prompt, Line: ev.Line, Cursor: utf8.RuneCountInString(ev.Line)}
		t.out(t.r.Render(s) + t.r.Leave(s))
		t.start(ev.Line)
	case ev.Interrupt:
		t.out("^C" + t.r.Leave(before) + t.r.Render(t.ed.State()))
	case ev.Clear:
		t.r.Forget()
		t.clearOut(t.r.Render(t.ed.State()))
	case ev.Print != nil:
		t.out(t.r.Print(ev.Print, t.ed.State()))
	default:
		t.out(t.r.Render(t.ed.State()))
	}
}

// termWriter turns a command's output into terminal output: "\n" becomes
// "\r\n" (the screen is a terminal, not a log).
type termWriter struct{ t *Terminal }

func (w termWriter) Write(p []byte) (int, error) {
	w.t.out(strings.ReplaceAll(string(p), "\n", "\r\n"))
	return len(p), nil
}

// start runs a line; t.mu is held.
func (t *Terminal) start(line string) {
	if strings.TrimSpace(line) == "" {
		t.out(t.r.Render(t.ed.State()))
		return
	}
	if t.shell == nil {
		t.out("no shell attached\r\n" + t.r.Render(t.ed.State()))
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.run = cancel
	t.app.post(func() { t.Running.Set(true) })
	shell := t.shell
	go func() {
		code, clear := shell.Run(ctx, line, termWriter{t})
		cancel()
		t.mu.Lock()
		t.run = nil
		var b strings.Builder
		if clear {
			t.r.Forget()
		} else if code != 0 {
			fmt.Fprintf(&b, "\x1b[2m[exit status %d]\x1b[0m\r\n", code)
		}
		b.WriteString(t.r.Render(t.ed.State()))
		t.mu.Unlock()
		out := b.String()
		t.app.post(func() {
			if clear {
				t.clear()
			}
			t.write(out)
			t.Running.Set(false)
			t.app.Refresh() // what the command changed shows in the views
		})
	}()
}

// clearOut clears the screen, then writes s (from any goroutine).
func (t *Terminal) clearOut(s string) {
	t.app.post(func() {
		t.clear()
		t.write(s)
	})
}

// clear runs on the UI goroutine: the view's Clear (screen and history),
// or the escape sequences that clear the screen.
func (t *Terminal) clear() {
	if t.Clear != nil {
		t.Clear()
		return
	}
	t.write("\x1b[H\x1b[2J")
}
