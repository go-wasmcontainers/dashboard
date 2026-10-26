package vm

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-wasmcontainers/engine/api"
	"github.com/go-widgets/mvvm"
)

// ContainerRow is one container as the table shows it. Every string is
// already cleaned (see Clean).
type ContainerRow struct {
	ID, Name, Image, Command string
	State, Status, Created   string
	CreatedAt                int64
}

// Short is the 12-character ID.
func (r ContainerRow) Short() string { return ShortID(r.ID) }

// Running reports whether the container runs.
func (r ContainerRow) Running() bool { return r.State == "running" }

// Ref is the name the command lines use: the name, else the short ID.
func (r ContainerRow) Ref() string {
	if r.Name != "" {
		return r.Name
	}
	return r.Short()
}

// ExitCode parses the exit status out of an "Exited (N) …" status.
func (r ContainerRow) ExitCode() (int, bool) {
	rest, ok := strings.CutPrefix(r.Status, "Exited (")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest[:max(strings.IndexByte(rest, ')'), 0)])
	return n, err == nil
}

// Container table columns.
const (
	ColName = iota
	ColImage
	ColState
	ColStatus
	ColCreated
	ColID
	NumContainerCols
)

// ContainerColumns are the column titles.
var ContainerColumns = [NumContainerCols]string{"Name", "Image", "State", "Status", "Created", "ID"}

// Cells is the row as table cells, in ContainerColumns order.
func (r ContainerRow) Cells() []string {
	return []string{r.Name, r.Image, r.State, r.Status, r.Created, r.Short()}
}

// MaxContainers bounds the rows the dashboard keeps.
const MaxContainers = 2000

// Containers is the containers view.
type Containers struct {
	app *App
	all []ContainerRow

	// Rows are the containers shown (filtered, sorted).
	Rows *mvvm.ObservableList[ContainerRow]
	// ShowAll shows stopped containers too (`wctr ps -a`).
	ShowAll *mvvm.Observable[bool]
	// Search filters by name, image, ID or command.
	Search *mvvm.Observable[string]
	// SortCol / SortAsc order the rows.
	SortCol *mvvm.Observable[int]
	SortAsc *mvvm.Observable[bool]
	// Selected is the selected container's ID ("" for none).
	Selected *mvvm.Observable[string]
}

func newContainers(a *App) *Containers {
	c := &Containers{
		app:      a,
		Rows:     mvvm.NewObservableList[ContainerRow](),
		ShowAll:  mvvm.NewObservable(true),
		Search:   mvvm.NewObservable(""),
		SortCol:  mvvm.NewObservable(ColCreated),
		SortAsc:  mvvm.NewObservable(false),
		Selected: mvvm.NewObservable(""),
	}
	for _, o := range []mvvm.Changeable{c.ShowAll, c.Search, c.SortCol, c.SortAsc} {
		o.SubscribeChanged(c.refilter)
	}
	return c
}

func (c *Containers) apply(list []api.ContainerSummary, now time.Time) {
	if len(list) > MaxContainers {
		list = list[:MaxContainers]
	}
	c.all = c.all[:0]
	for _, s := range list {
		name := ""
		if len(s.Names) > 0 {
			name = strings.TrimPrefix(s.Names[0], "/")
		}
		c.all = append(c.all, ContainerRow{
			ID: Clean(s.ID, 128), Name: Clean(name, maxField), Image: Clean(s.Image, maxField),
			Command: Clean(s.Command, maxField), State: Clean(s.State, 32), Status: Clean(s.Status, 64),
			Created: Ago(now, time.Unix(s.Created, 0)), CreatedAt: s.Created,
		})
	}
	c.refilter()
}

// Find returns the row of the container with id (any shown or hidden).
func (c *Containers) Find(id string) (ContainerRow, bool) {
	for _, r := range c.all {
		if r.ID == id {
			return r, true
		}
	}
	return ContainerRow{}, false
}

func (c *Containers) refilter() {
	q := strings.ToLower(strings.TrimSpace(c.Search.Get()))
	var rows []ContainerRow
	for _, r := range c.all {
		if !c.ShowAll.Get() && !r.Running() {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(r.Name+" "+r.Image+" "+r.ID+" "+r.Command+" "+r.State), q) {
			continue
		}
		rows = append(rows, r)
	}
	col, asc := c.SortCol.Get(), c.SortAsc.Get()
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		var less, eq bool
		if col == ColCreated {
			less, eq = a.CreatedAt < b.CreatedAt, a.CreatedAt == b.CreatedAt
		} else {
			x, y := a.Cells()[col], b.Cells()[col]
			less, eq = x < y, x == y
		}
		if eq {
			return a.ID < b.ID
		}
		return less == asc
	})
	Sync(c.Rows, rows, func(r ContainerRow) string { return r.ID })
	if _, ok := c.Find(c.Selected.Get()); !ok {
		c.Selected.Set("")
	}
}

// Counts is how many containers run and exist.
func (c *Containers) Counts() (running, total int) {
	for _, r := range c.all {
		if r.Running() {
			running++
		}
	}
	return running, len(c.all)
}

// StartLine, StopLine, RestartLine and RemoveLine are the command lines
// the actions stand for.
func (c *Containers) StartLine(r ContainerRow) string {
	return c.app.cli.Equivalent("POST /containers/"+r.Ref()+"/start", "start", r.Ref())
}

// StopLine is `wctr stop` (a wasm guest has no signals: it stops at once).
func (c *Containers) StopLine(r ContainerRow) string {
	return c.app.cli.Equivalent("POST /containers/"+r.Ref()+"/stop", "stop", r.Ref())
}

// RestartLine is `wctr restart`.
func (c *Containers) RestartLine(r ContainerRow) string {
	return c.app.cli.Equivalent("POST /containers/"+r.Ref()+"/restart", "restart", r.Ref())
}

// RemoveLine is rm, forced for a running container.
func (c *Containers) RemoveLine(r ContainerRow) string {
	if r.Running() {
		return Line("rm", "-f", r.Ref())
	}
	return Line("rm", r.Ref())
}

// Start starts the container.
func (c *Containers) Start(r ContainerRow) {
	c.app.Do(c.StartLine(r), func(ctx context.Context) error { return c.app.eng.Start(ctx, r.ID) })
}

// Stop stops the container; the engine answers once it has exited, so
// the refresh that follows shows it stopped.
func (c *Containers) Stop(r ContainerRow) {
	c.app.Do(c.StopLine(r), func(ctx context.Context) error { return c.app.eng.Stop(ctx, r.ID) })
}

// Restart stops the container if it runs and starts it again.
func (c *Containers) Restart(r ContainerRow) {
	c.app.Do(c.RestartLine(r), func(ctx context.Context) error { return c.app.eng.Restart(ctx, r.ID) })
}

// Remove removes the container (forced when it runs).
func (c *Containers) Remove(r ContainerRow) {
	c.app.Do(c.RemoveLine(r), func(ctx context.Context) error { return c.app.eng.Remove(ctx, r.ID, r.Running()) })
}
