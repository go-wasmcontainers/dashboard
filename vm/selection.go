package vm

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/go-widgets/mvvm"
)

// Job is one row's share of a multi-row action: the command line it stands
// for, the name its failure is reported under, and the call itself.
type Job struct {
	Line, Name string
	Run        func(ctx context.Context) error
}

// actionTimeout bounds each engine call an action makes.
const actionTimeout = 5 * time.Minute

// maxHover is how many commands a hover line spells out before it counts
// the rest.
const maxHover = 3

// JoinLines is the one-line form of the commands an action will run: the
// command itself for one row, else the count and the first few.
func JoinLines(lines []string) string {
	switch len(lines) {
	case 0:
		return ""
	case 1:
		return lines[0]
	}
	head := lines
	more := ""
	if len(head) > maxHover {
		head, more = head[:maxHover], fmt.Sprintf("; … +%d more", len(lines)-maxHover)
	}
	return fmt.Sprintf("%d commands: %s%s", len(lines), strings.Join(head, "; "), more)
}

// DoAll runs the jobs one after the other. A failing job is reported and
// does not stop the others; the dashboard refreshes once, at the end.
func (a *App) DoAll(jobs []Job) {
	switch len(jobs) {
	case 0:
		return
	case 1:
		a.Do(jobs[0].Line, jobs[0].Run)
		return
	}
	lines := make([]string, len(jobs))
	for i, j := range jobs {
		lines[i] = j.Line
		a.Terminal.note(j.Line)
	}
	a.LastCommand.Set(JoinLines(lines))
	a.Error.Set("")
	a.Pending.Set(a.Pending.Get() + 1)
	go func() {
		var failed []string
		for _, j := range jobs {
			ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
			err := j.Run(ctx)
			cancel()
			if err != nil {
				failed = append(failed, j.Name+": "+Clean(err.Error(), 200))
			}
		}
		a.post(func() {
			a.Pending.Set(a.Pending.Get() - 1)
			if len(failed) > 0 {
				a.Error.Set(Clean(fmt.Sprintf("%d of %d failed: %s", len(failed), len(jobs), strings.Join(failed, "; ")), 500))
			}
			a.Refresh()
		})
	}()
}

// newSet is the observable holding a view's selected keys.
func newSet() *mvvm.Observable[[]string] {
	return mvvm.NewObservableEq[[]string](nil, slices.Equal[[]string])
}

// pruneSet keeps the keys of set that name a shown row.
func pruneSet[R any](set *mvvm.Observable[[]string], rows *mvvm.ObservableList[R], key func(R) string) {
	have := make(map[string]bool, rows.Len())
	for i := 0; i < rows.Len(); i++ {
		have[key(rows.At(i))] = true
	}
	var keep []string
	for _, k := range set.Get() {
		if have[k] {
			keep = append(keep, k)
		}
	}
	set.Set(keep)
}

// pick is the rows an action acts on: the selected set, else the anchor
// row alone, in the order of the selection. Hidden rows are never acted on.
func pick[R any](set []string, anchor string, rows *mvvm.ObservableList[R], key func(R) string) []R {
	keys := set
	if len(keys) == 0 && anchor != "" {
		keys = []string{anchor}
	}
	var out []R
	for _, k := range keys {
		for i := 0; i < rows.Len(); i++ {
			if r := rows.At(i); key(r) == k {
				out = append(out, r)
				break
			}
		}
	}
	return out
}
