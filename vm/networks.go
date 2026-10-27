package vm

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/go-wasmcontainers/engine/api"
	"github.com/go-widgets/mvvm"

	"github.com/go-wasmcontainers/dashboard/engine"
)

// NetworkRow is one network.
type NetworkRow struct {
	Name, ID, Driver, Subnet string
	// Containers is how many containers are attached, Attached who they
	// are and their addresses ("web 10.42.0.2, db 10.42.0.3").
	Containers int
	Attached   string
}

// NetworkColumns are the column titles.
var NetworkColumns = []string{"Name", "ID", "Driver", "Subnet", "Containers"}

// Cells is the row as table cells.
func (r NetworkRow) Cells() []string {
	n := fmt.Sprint(r.Containers)
	if r.Attached != "" {
		n += ": " + r.Attached
	}
	return []string{r.Name, r.ID, r.Driver, r.Subnet, n}
}

// maxAttached bounds the attached containers listed in one cell.
const maxAttached = 8

// Networks is the networks view: list, create, inspect, remove, each
// naming its `wctr network …` line. An engine that does not serve GET
// /networks gets a message saying so, never invented rows.
type Networks struct {
	app       *App
	Rows      *mvvm.ObservableList[NetworkRow]
	Supported *mvvm.Observable[int]
	// Message explains an empty or unavailable view.
	Message *mvvm.Observable[string]
	// Selected is the selected network's name.
	Selected *mvvm.Observable[string]
	// NewName is the create field.
	NewName *mvvm.Observable[string]
	// Inspect is the selected network's inspect document as a tree.
	Inspect *mvvm.ObservableList[InspectNode]
}

func newNetworks(a *App) *Networks {
	n := &Networks{app: a, Rows: mvvm.NewObservableList[NetworkRow](),
		Supported: mvvm.NewObservable(VolumesUnknown), Message: mvvm.NewObservable(""),
		Selected: mvvm.NewObservable(""), NewName: mvvm.NewObservable(""),
		Inspect: mvvm.NewObservableList[InspectNode]()}
	n.Selected.Subscribe(func(string) { n.inspect() })
	return n
}

func (n *Networks) apply(list []engine.Network, err error, version string) {
	switch {
	case errors.Is(err, engine.ErrUnsupported):
		n.Supported.Set(VolumesUnsupported)
		n.Message.Set("Networks require an engine that serves GET /networks; engine " + Clean(version, 40) + " does not.")
		n.Rows.Clear()
		return
	case err != nil:
		n.Message.Set("networks: " + Clean(err.Error(), maxField))
		return
	}
	n.Supported.Set(VolumesSupported)
	rows := make([]NetworkRow, 0, len(list))
	for _, x := range list {
		r := NetworkRow{Name: Clean(x.Name, maxField), ID: ShortID(x.ID), Driver: Clean(x.Driver, 64), Containers: len(x.Containers)}
		var subnets []string
		for _, c := range x.IPAM.Config {
			if s := c["Subnet"]; s != "" {
				subnets = append(subnets, Clean(s, 64))
			}
		}
		r.Subnet = strings.Join(subnets, ", ")
		var who []string
		for id, ep := range x.Containers {
			name := ep.Name
			if name == "" {
				name = ShortID(id)
			}
			who = append(who, Clean(name, 64)+" "+Clean(ep.IPv4Address, 64))
		}
		sort.Strings(who)
		if len(who) > maxAttached {
			who = append(who[:maxAttached], fmt.Sprintf("+%d more", len(who)-maxAttached))
		}
		r.Attached = strings.Join(who, ", ")
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	Sync(n.Rows, rows, func(r NetworkRow) string { return r.Name })
	if len(rows) == 0 {
		n.Message.Set("No networks. Create one here, then run a container with --network NAME.")
	} else {
		n.Message.Set("")
	}
	if _, ok := n.Find(n.Selected.Get()); !ok {
		n.Selected.Set("")
	} else {
		n.inspect() // attachments change while it stays selected
	}
}

// Find returns the row of the network name.
func (n *Networks) Find(name string) (NetworkRow, bool) {
	for i := 0; i < n.Rows.Len(); i++ {
		if r := n.Rows.At(i); r.Name == name {
			return r, true
		}
	}
	return NetworkRow{}, false
}

func (n *Networks) inspect() {
	name := n.Selected.Get()
	if name == "" {
		n.Inspect.Clear()
		return
	}
	go func() {
		raw, err := n.app.eng.NetworkInspect(context.Background(), name)
		n.app.post(func() {
			if name != n.Selected.Get() {
				return
			}
			tree, terr := InspectTree(raw)
			switch {
			case err != nil:
				tree = []InspectNode{{Key: "error", Value: Clean(err.Error(), maxField)}}
			case terr != nil:
				tree = []InspectNode{{Key: "error", Value: Clean(terr.Error(), maxField)}}
			}
			replace(n.Inspect, tree)
		})
	}()
}

// InspectLine is the command line the inspect pane stands for.
func (n *Networks) InspectLine(name string) string { return Line("network", "inspect", name) }

// CreateLine is the create field's command line.
func (n *Networks) CreateLine() string {
	return Line("network", "create", strings.TrimSpace(n.NewName.Get()))
}

// Create creates a network named NewName (an in-engine bridge).
func (n *Networks) Create() {
	name := strings.TrimSpace(n.NewName.Get())
	if name == "" {
		n.app.Error.Set("a network needs a name")
		return
	}
	n.app.Do(n.CreateLine(), func(ctx context.Context) error {
		_, err := n.app.eng.NetworkCreate(ctx, api.NetworkCreateRequest{Name: name})
		if err == nil {
			n.app.post(func() { n.NewName.Set("") })
		}
		return err
	})
}

// RemoveLine is the command line of Remove.
func (n *Networks) RemoveLine(name string) string { return Line("network", "rm", name) }

// Remove removes a network.
func (n *Networks) Remove(name string) {
	n.app.Do(n.RemoveLine(name), func(ctx context.Context) error {
		return n.app.eng.NetworkRemove(ctx, name)
	})
}
