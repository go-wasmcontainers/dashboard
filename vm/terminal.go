package vm

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/go-wasmcontainers/engine/cli"
	"github.com/go-wasmcontainers/engine/cli/lineedit"
	"github.com/go-widgets/mvvm"
	"github.com/go-widgets/shellterm"
	"github.com/spf13/cobra"
)

// Shell runs one command line, writing its output to out, and returns the
// exit status and whether the screen should be cleared: shellterm's. The
// playground's session has this shape; WctrShell is the dashboard's own.
type Shell = shellterm.Shell

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

// Terminal is the terminal view's model: a go-widgets/shellterm session
// (the line editor, history, completion, Ctrl-C, Ctrl-L, in front of a
// Shell; shared with wasmdesk, go-widgets/toolkit#548) plus the dashboard's
// own echo of its actions. SetShell, Attach, Input, Resize and Running are
// the session's.
type Terminal struct {
	*shellterm.Session
	// ShowActions echoes each dashboard action's command line here.
	ShowActions *mvvm.Observable[bool]
}

// greeting is written once, before the first prompt.
const greeting = "\x1b[1mwctr\x1b[0m in the dashboard: WebAssembly containers, same engine as the views. Type \x1b[1mhelp\x1b[0m; Tab completes.\r\n"

func newTerminal(a *App) *Terminal {
	return &Terminal{
		Session: shellterm.New(shellterm.Options{
			Post:        a.post,
			Greeting:    greeting,
			OnDone:      a.Refresh, // what the command changed shows in the views
			HistorySize: 500,
		}),
		ShowActions: mvvm.NewObservable(true),
	}
}

// note shows a dashboard action's command line above the prompt, cleaned
// (it may carry a name the engine reported). The session shows nothing
// while a command runs or before the view is attached. Called on the UI
// goroutine, which it never blocks.
func (t *Terminal) note(line string) {
	if !t.ShowActions.Get() {
		return
	}
	t.Note("\x1b[2m# dashboard: " + Clean(line, 500) + "\x1b[0m")
}
