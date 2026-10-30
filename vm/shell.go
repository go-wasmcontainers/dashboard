package vm

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/go-wasmcontainers/engine/api"
	"github.com/go-widgets/mvvm"
)

// ShellCmd is what Open runs in the container. (An exec into a container
// that has started but has no process yet waits for it in the engine,
// v0.29.1, so there is nothing to retry here.)
var ShellCmd = []string{"/bin/sh"}

// Shell bounds: input chunks queued for the shell, and the output read at
// a time.
const (
	maxShellQueue = 256
	shellChunk    = 32 << 10
)

// ExecShell is an interactive shell inside a container (`wctr exec -it
// CONTAINER sh`): POST /containers/{id}/exec with a terminal, then
// /exec/{id}/start with the keys as stdin and the raw output streamed back.
// The engine does the line discipline and echo; the view's terminal shows
// the output (its screen discards every OSC/DCS/APC string) and hands the
// keys back as bytes.
type ExecShell struct {
	app *App

	// Line is the command line this stands for; Status says how it goes:
	// starting, running, "exited with status N", or the engine's error.
	Line    *mvvm.Observable[string]
	Status  *mvvm.Observable[string]
	Running *mvvm.Observable[bool]
	// Container is the ID of the container the shell is in ("" none).
	Container *mvvm.Observable[string]

	// Out receives the output and Reset clears the terminal; both are set
	// by the view and called on the UI goroutine.
	Out   func([]byte)
	Reset func()

	cols, rows int
	gen        int
	s          *session
}

// session is one exec instance, from start to hang-up.
type session struct {
	id     string
	in     chan []byte
	stop   context.CancelFunc
	once   sync.Once
	closed chan struct{}

	mu    sync.Mutex
	stdin *io.PipeReader // the request's side of the keys
}

// end hangs up: stdin ends (the shell reads EOF), its reader side is
// closed (a key being written returns), and the request is cancelled,
// which closes the stream and kills the process.
func (s *session) end() {
	s.once.Do(func() {
		close(s.closed)
		s.mu.Lock()
		if s.stdin != nil {
			s.stdin.CloseWithError(io.ErrClosedPipe)
		}
		s.mu.Unlock()
		s.stop()
	})
}

func newExecShell(a *App) *ExecShell {
	return &ExecShell{app: a, Line: mvvm.NewObservable(""), Status: mvvm.NewObservable(""),
		Running: mvvm.NewObservable(false), Container: mvvm.NewObservable(""), cols: 80, rows: 24}
}

// Open starts a shell in container id (shown as name), at the terminal's
// size. A shell already running there is kept.
func (sh *ExecShell) Open(id, name string) {
	if sh.s != nil && sh.Container.Get() == id {
		return
	}
	sh.Close()
	sh.gen++
	gen := sh.gen
	if sh.Reset != nil {
		sh.Reset()
	}
	sh.Container.Set(id)
	sh.Line.Set(Line("exec", "-it", name, "sh"))
	sh.app.LastCommand.Set(sh.Line.Get())
	sh.app.Terminal.note(sh.Line.Get())
	sh.Status.Set("starting " + ShellCmd[0] + " in " + Clean(name, 64) + "…")
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{in: make(chan []byte, maxShellQueue), stop: cancel, closed: make(chan struct{})}
	sh.s = s
	size := []uint16{uint16(sh.rows), uint16(sh.cols)}
	go sh.run(ctx, gen, s, id, size)
}

// run creates and starts the exec, then copies its output to the view
// until it ends, and says how it ended.
func (sh *ExecShell) run(ctx context.Context, gen int, s *session, container string, size []uint16) {
	fail := func(err error) {
		msg := Clean(err.Error(), maxField)
		sh.app.post(func() {
			if gen == sh.gen {
				sh.Status.Set("exec: " + msg)
				sh.Running.Set(false)
				sh.s = nil
			}
		})
	}
	req := api.ExecCreateRequest{Cmd: ShellCmd, Tty: true, AttachStdin: true, AttachStdout: true, AttachStderr: true, ConsoleSize: size}
	id, err := sh.app.eng.ExecCreate(ctx, container, req)
	if err != nil {
		fail(err)
		s.end()
		return
	}
	sh.app.post(func() { s.id = id }) // read by Resize, on the UI goroutine
	pr, pw := io.Pipe()
	s.mu.Lock()
	s.stdin = pr
	s.mu.Unlock()
	go func() { // the keys, in order, until the session ends
		defer pw.Close()
		for {
			select {
			case b := <-s.in:
				// After a hang-up the reader side is closed, so this returns
				// at once (with an error), and the next select sees closed.
				_, _ = pw.Write(b)
			case <-s.closed:
				return
			}
		}
	}()
	out, err := sh.app.eng.ExecStart(ctx, id, api.ExecStartRequest{Tty: true, ConsoleSize: size}, pr)
	if err != nil {
		fail(err)
		s.end()
		return
	}
	sh.app.post(func() {
		if gen == sh.gen {
			sh.Status.Set("running")
			sh.Running.Set(true)
		}
	})
	buf := make([]byte, shellChunk)
	for {
		n, err := out.Read(buf)
		if n > 0 {
			b := append([]byte(nil), buf[:n]...)
			sh.app.post(func() {
				if gen == sh.gen && sh.Out != nil {
					sh.Out(b)
				}
			})
		}
		if err != nil {
			break
		}
	}
	out.Close()
	s.end()
	status := "ended"
	if ins, err := sh.app.eng.ExecInspect(context.Background(), id); err == nil && ins.ExitCode != nil {
		status = fmt.Sprintf("exited with status %d", *ins.ExitCode)
	}
	sh.app.post(func() {
		if gen == sh.gen {
			sh.Status.Set(status)
			sh.Running.Set(false)
			sh.s = nil
		}
	})
}

// Input sends keys (the terminal's bytes) to the shell. Keys typed before
// it runs wait (bounded); past the bound they are dropped, and said so.
func (sh *ExecShell) Input(keys string) {
	if sh.s == nil || keys == "" {
		return
	}
	select {
	case sh.s.in <- []byte(keys):
	default:
		sh.Status.Set("input dropped: the shell is not reading")
	}
}

// Resize follows the terminal's size, and tells a running shell.
func (sh *ExecShell) Resize(cols, rows int) {
	if cols <= 0 || rows <= 0 || cols > 1000 || rows > 1000 || cols == sh.cols && rows == sh.rows {
		return
	}
	sh.cols, sh.rows = cols, rows
	if s := sh.s; s != nil && s.id != "" {
		id := s.id
		go sh.app.eng.ExecResize(context.Background(), id, uint16(rows), uint16(cols))
	}
}

// Close hangs up the shell, if one runs.
func (sh *ExecShell) Close() {
	if sh.s != nil {
		sh.s.end()
		sh.s = nil
	}
	sh.Running.Set(false)
}
