package vm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/go-wasmcontainers/engine/api"
	"github.com/go-widgets/mvvm"
)

// InspectNode is one line of the inspect tree, in document order.
type InspectNode struct {
	Depth      int
	Key, Value string
	// Branch is an object or array (its children follow it, one deeper).
	Branch bool
}

// Bounds on the inspect tree.
const (
	maxInspectNodes = 5000
	maxInspectDepth = 32
)

// InspectTree flattens a JSON document into tree lines, keeping the
// document's key order; values are cleaned, the tree is bounded.
func InspectTree(doc []byte) ([]InspectNode, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var out []InspectNode
	var walk func(depth int, key string) error
	walk = func(depth int, key string) error {
		if len(out) >= maxInspectNodes || depth > maxInspectDepth {
			return errors.New("inspect document too large to show")
		}
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case json.Delim:
			kind := "{}"
			if t == '[' {
				kind = "[]"
			}
			i := len(out)
			out = append(out, InspectNode{Depth: depth, Key: key, Branch: true})
			n := 0
			for dec.More() {
				k := fmt.Sprint(n)
				if t == '{' {
					kt, err := dec.Token()
					if err != nil {
						return err
					}
					k = Clean(fmt.Sprint(kt), maxField)
				}
				if err := walk(depth+1, k); err != nil {
					return err
				}
				n++
			}
			if _, err := dec.Token(); err != nil {
				return err
			}
			out[i].Value = fmt.Sprintf("%c%d%c", kind[0], n, kind[1])
		case string:
			out = append(out, InspectNode{Depth: depth, Key: key, Value: Clean(fmt.Sprintf("%q", t), 500)})
		case nil:
			out = append(out, InspectNode{Depth: depth, Key: key, Value: "null"})
		default:
			out = append(out, InspectNode{Depth: depth, Key: key, Value: fmt.Sprint(t)})
		}
		return nil
	}
	if err := walk(0, ""); err != nil {
		return nil, err
	}
	return out, nil
}

// maxPendingLog bounds log bytes handed to the UI and not yet drawn; past
// it, output is dropped (and said so) rather than queued without end.
const maxPendingLog = 2 << 20

// Detail is the container detail pane: inspect tree, environment, mounts,
// logs.
type Detail struct {
	app *App

	// ID is the container shown ("" for none).
	ID    *mvvm.Observable[string]
	Title *mvvm.Observable[string]
	State *mvvm.Observable[string]
	Tree  *mvvm.ObservableList[InspectNode]
	Env   *mvvm.ObservableList[string]
	// Mounts lists "source -> destination"; empty with a note when none.
	Mounts *mvvm.ObservableList[string]
	// Follow streams new log output as it comes (`wctr logs -f`).
	Follow *mvvm.Observable[bool]
	// LogsLine is the command line the logs pane stands for.
	LogsLine *mvvm.Observable[string]

	// LogOut receives log output (already demultiplexed; stderr in red)
	// and LogReset clears the log pane; both are set by the view and
	// called on the UI goroutine.
	LogOut   func([]byte)
	LogReset func()

	cancel  context.CancelFunc
	pending atomic.Int64
	gen     int
}

func newDetail(a *App) *Detail {
	d := &Detail{
		app: a, ID: mvvm.NewObservable(""), Title: mvvm.NewObservable(""), State: mvvm.NewObservable(""),
		Tree: mvvm.NewObservableList[InspectNode](), Env: mvvm.NewObservableList[string](),
		Mounts: mvvm.NewObservableList[string](), Follow: mvvm.NewObservable(true), LogsLine: mvvm.NewObservable(""),
	}
	d.Follow.SubscribeChanged(func() {
		if d.ID.Get() != "" {
			d.streamLogs()
		}
	})
	return d
}

// Open shows container id: inspects it and streams its logs.
func (d *Detail) Open(id string) {
	if id == d.ID.Get() {
		return
	}
	d.Close()
	d.ID.Set(id)
	if id == "" {
		return
	}
	r, _ := d.app.Containers.Find(id)
	d.Title.Set(r.Ref())
	d.inspect()
	d.streamLogs()
}

// Close stops the log stream and clears the pane.
func (d *Detail) Close() {
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
	d.gen++
	d.ID.Set("")
	d.Title.Set("")
	d.State.Set("")
	d.Tree.Clear()
	d.Env.Clear()
	d.Mounts.Clear()
	d.LogsLine.Set("")
	if d.LogReset != nil {
		d.LogReset()
	}
}

// refresh re-inspects the shown container (its state may have changed),
// or closes the pane when the container is gone.
func (d *Detail) refresh() {
	id := d.ID.Get()
	if id == "" {
		return
	}
	if _, ok := d.app.Containers.Find(id); !ok {
		d.Close()
		return
	}
	d.inspect()
}

func (d *Detail) inspect() {
	id := d.ID.Get()
	go func() {
		raw, err := d.app.eng.InspectContainer(context.Background(), id)
		d.app.post(func() {
			if id != d.ID.Get() {
				return // another container is shown now
			}
			if err != nil {
				d.State.Set("inspect: " + Clean(err.Error(), maxField))
				return
			}
			d.applyInspect(raw)
		})
	}()
}

func (d *Detail) applyInspect(raw []byte) {
	tree, err := InspectTree(raw)
	if err != nil {
		d.State.Set("inspect: " + Clean(err.Error(), maxField))
		return
	}
	var ins struct {
		api.ContainerInspect
		Mounts []struct {
			Source, Destination string
		} `json:"Mounts"`
	}
	_ = json.Unmarshal(raw, &ins) // the tree parsed it already
	st := ins.State
	state := Clean(st.Status, 32)
	if !st.Running && st.FinishedAt != "" {
		state += fmt.Sprintf(" · exit code %d", st.ExitCode)
	}
	if st.Error != "" {
		state += " · " + Clean(st.Error, maxField)
	}
	d.State.Set(state)
	replace(d.Tree, tree)
	env := make([]string, 0, len(ins.Config.Env))
	for _, e := range ins.Config.Env {
		env = append(env, Clean(e, 500))
	}
	sort.Strings(env)
	replace(d.Env, env)
	var mounts []string
	for _, m := range ins.Mounts {
		mounts = append(mounts, Clean(m.Source+" -> "+m.Destination, 500))
	}
	replace(d.Mounts, mounts)
}

// replace sets l to want when it differs (an ordered document: no keys).
func replace[T comparable](l *mvvm.ObservableList[T], want []T) {
	if slices.Equal(l.Slice(), want) {
		return
	}
	l.Clear()
	l.Append(want...)
}

// logWriter hands one stream's bytes to the UI, bounded.
type logWriter struct {
	d      *Detail
	gen    int
	stderr bool
}

func (w logWriter) Write(p []byte) (int, error) {
	d := w.d
	if d.pending.Add(int64(len(p))) > maxPendingLog {
		d.pending.Add(-int64(len(p)))
		return len(p), nil
	}
	b := append([]byte(nil), p...)
	if w.stderr {
		b = append(append([]byte("\x1b[31m"), b...), "\x1b[0m"...)
	}
	d.app.post(func() {
		d.pending.Add(-int64(len(p)))
		if w.gen == d.gen && d.LogOut != nil {
			d.LogOut(b)
		}
	})
	return len(p), nil
}

func (d *Detail) streamLogs() {
	if d.cancel != nil {
		d.cancel()
	}
	d.gen++
	if d.LogReset != nil {
		d.LogReset()
	}
	id, gen, follow := d.ID.Get(), d.gen, d.Follow.Get()
	r, _ := d.app.Containers.Find(id)
	args := []string{"logs"}
	if follow {
		args = append(args, "-f")
	}
	d.LogsLine.Set(Line(append(args, r.Ref())...))
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	go func() {
		rc, err := d.app.eng.Logs(ctx, id, follow)
		if err == nil {
			err = api.Demux(logWriter{d, gen, false}, logWriter{d, gen, true}, rc)
			rc.Close()
		}
		if err != nil && ctx.Err() == nil && !errors.Is(err, io.EOF) {
			msg := []byte("\r\n\x1b[2m[logs: " + strings.ReplaceAll(Clean(err.Error(), maxField), "\x1b", "") + "]\x1b[0m\r\n")
			d.app.post(func() {
				if gen == d.gen && d.LogOut != nil {
					d.LogOut(msg)
				}
			})
		}
	}()
}
