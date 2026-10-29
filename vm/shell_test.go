package vm

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-wasmcontainers/engine/api"
)

func TestShell(t *testing.T) {
	h := newHarness(t)
	h.f.set(func() {
		h.f.containers = []api.ContainerSummary{{ID: "c1", Names: []string{"/web"}, State: "running"}}
	})
	h.refresh()
	sh := h.app.ExecShell
	var out strings.Builder
	resets := 0
	sh.Out = func(b []byte) { out.Write(b) }
	sh.Reset = func() { resets++ }

	// Keys before a shell: nowhere to go.
	sh.Input("ignored")
	sh.Resize(100, 30)
	sh.Open("c1", "web")
	if sh.Line.Get() != "wctr exec -it web sh" || h.app.LastCommand.Get() != sh.Line.Get() ||
		!strings.HasPrefix(sh.Status.Get(), "starting /bin/sh in web") || resets != 1 {
		t.Fatalf("open: %q %q %d", sh.Line.Get(), sh.Status.Get(), resets)
	}
	sh.Input("echo hi\r") // typed while it starts: queued
	h.until("running", func() bool { return sh.Running.Get() && strings.Contains(out.String(), "> echo hi\r\n") })
	s := get(h.f, func() *execSession { return h.f.execs["e1"] })
	if !s.req.Tty || !s.req.AttachStdin || !s.req.AttachStdout || !s.req.AttachStderr ||
		len(s.req.ConsoleSize) != 2 || s.req.ConsoleSize[0] != 30 || s.req.ConsoleSize[1] != 100 ||
		strings.Join(s.req.Cmd, " ") != "/bin/sh" || !h.f.called("exec create c1 /bin/sh") {
		t.Errorf("exec create = %+v", s.req)
	}
	sh.Open("c1", "web") // already there: kept
	if resets != 1 {
		t.Error("reopening the same container restarted the shell")
	}
	// Resize: told to the running shell; the same size or nonsense is not.
	sh.Resize(120, 40)
	sh.Resize(120, 40)
	sh.Resize(0, 10)
	sh.Resize(5000, 10)
	h.until("resized", func() bool { return h.f.called("exec resize e1 120x40") })
	// exit 3: the status says so.
	sh.Input("exit 3\r")
	h.until("exited", func() bool { return !sh.Running.Get() && sh.Status.Get() == "exited with status 3" })
	sh.Input("after the end")
	h.settle()

	// The engine refuses: its message is shown (no /bin/sh, not running).
	h.f.setErr("exec create c1 /bin/sh", errors.New("container web is not running"))
	sh.Close()
	sh.Open("c1", "web")
	h.until("refused", func() bool { return sh.Status.Get() == "exec: container web is not running" && !sh.Running.Get() })
	h.f.setErr("exec create c1 /bin/sh", nil)
	h.f.setErr("exec start e1", errors.New("no such exec"))
	sh.Open("c1", "web")
	h.until("start refused", func() bool { return sh.Status.Get() == "exec: no such exec" })
	h.f.setErr("exec start e1", nil)

	// Hanging up: closing the detail ends the shell (stdin EOF: status 0).
	sh.Open("c1", "web")
	h.until("running again", func() bool { return sh.Running.Get() })
	h.app.Detail.Open("c1")
	h.app.Detail.Close()
	h.until("hung up", func() bool { return !sh.Running.Get() && strings.HasPrefix(sh.Status.Get(), "exited") })
	// An inspect that fails: "ended".
	h.f.setErr("exec inspect e1", errors.New("gone"))
	sh.Open("c1", "web")
	h.until("running 3", func() bool { return sh.Running.Get() })
	sh.Input("exit 0\r")
	h.until("ended", func() bool { return sh.Status.Get() == "ended" })

	// Right after a start: refused twice, then taken.
	old := ExecRetryDelay
	ExecRetryDelay = time.Millisecond
	defer func() { ExecRetryDelay = old }()
	h.f.setErr("exec inspect e1", nil)
	h.f.set(func() { h.f.notYet = 2 })
	sh.Close()
	sh.Open("c1", "web")
	h.until("taken after retries", func() bool { return sh.Running.Get() })
	sh.Input("exit 0\r")
	h.until("done", func() bool { return !sh.Running.Get() })
	// Refused every time: the engine's message after the last try.
	h.f.set(func() { h.f.notYet = ExecRetries + 1 })
	sh.Open("c1", "web")
	h.until("given up", func() bool {
		return strings.HasPrefix(sh.Status.Get(), "exec: Error response from daemon: container is not running")
	})
	// Hung up while trying again: no more tries.
	ExecRetryDelay = time.Hour
	h.f.set(func() { h.f.notYet = 1 })
	sh.Open("c1", "web")
	sh.Close()
	h.until("hung up while waiting", func() bool { return strings.HasPrefix(sh.Status.Get(), "exec: ") })
	ExecRetryDelay = time.Millisecond
	h.f.set(func() { h.f.notYet = 0 })

	// A full queue drops input, and says so.
	h.f.setErr("exec inspect e1", nil)
	sh.Close()
	sh.s = &session{in: make(chan []byte, 1), closed: make(chan struct{}), stop: func() {}}
	sh.Input("a")
	sh.Input("b")
	if sh.Status.Get() != "input dropped: the shell is not reading" {
		t.Errorf("full queue: %q", sh.Status.Get())
	}
	sh.Close()
	h.settle()
}
