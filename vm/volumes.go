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

// VolumeRow is one volume.
type VolumeRow struct {
	Name, Driver, Mountpoint, Created, Size string
	// Used is how many containers reference it ("" when unknown).
	Used string
}

// VolumeColumns are the column titles.
var VolumeColumns = []string{"Name", "Driver", "Mountpoint", "Created", "Size", "Used by"}

// Cells is the row as table cells.
func (r VolumeRow) Cells() []string {
	return []string{r.Name, r.Driver, r.Mountpoint, r.Created, r.Size, r.Used}
}

// Volume support, as the engine answered.
const (
	VolumesUnknown = iota
	VolumesUnsupported
	VolumesSupported
)

// Volumes is the volumes view: list, create, inspect, remove, prune, each
// naming its `wctr volume …` line. An engine that does not serve GET
// /volumes gets a message saying so, never invented rows.
type Volumes struct {
	app       *App
	Rows      *mvvm.ObservableList[VolumeRow]
	Supported *mvvm.Observable[int]
	// Message explains an empty or unavailable view.
	Message *mvvm.Observable[string]
	// Selected is the selected volume's name.
	Selected *mvvm.Observable[string]
	// NewName is the create field.
	NewName *mvvm.Observable[string]
	// Inspect is the selected volume's inspect document as a tree.
	Inspect *mvvm.ObservableList[InspectNode]
	// Notice is the last prune's result.
	Notice *mvvm.Observable[string]
}

func newVolumes(a *App) *Volumes {
	v := &Volumes{app: a, Rows: mvvm.NewObservableList[VolumeRow](), Notice: mvvm.NewObservable(""),
		Supported: mvvm.NewObservable(VolumesUnknown), Message: mvvm.NewObservable(""),
		Selected: mvvm.NewObservable(""), NewName: mvvm.NewObservable(""),
		Inspect: mvvm.NewObservableList[InspectNode]()}
	v.Selected.Subscribe(func(string) { v.inspect() })
	return v
}

func (v *Volumes) apply(list []engine.Volume, err error, version string, df *engine.DiskUsage) {
	switch {
	case errors.Is(err, engine.ErrUnsupported):
		v.Supported.Set(VolumesUnsupported)
		v.Message.Set("Volumes require an engine that serves GET /volumes; engine " + Clean(version, 40) +
			" does not.")
		v.Rows.Clear()
		return
	case err != nil:
		v.Message.Set("volumes: " + Clean(err.Error(), maxField))
		return
	}
	v.Supported.Set(VolumesSupported)
	usage := map[string]*api.VolumeUsage{}
	if df != nil {
		for _, x := range df.Volumes {
			usage[x.Name] = x.UsageData
		}
	}
	rows := make([]VolumeRow, 0, len(list))
	for _, x := range list {
		r := VolumeRow{Name: Clean(x.Name, maxField), Driver: Clean(x.Driver, 64),
			Mountpoint: Clean(x.Mountpoint, maxField), Created: Clean(x.CreatedAt, 64)}
		u := x.UsageData
		if u == nil {
			u = usage[x.Name]
		}
		if u != nil {
			if u.Size >= 0 {
				r.Size = Size(u.Size)
			}
			if u.RefCount >= 0 {
				r.Used = fmt.Sprintf("%d container(s)", u.RefCount)
			}
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	Sync(v.Rows, rows, func(r VolumeRow) string { return r.Name })
	if len(rows) == 0 {
		v.Message.Set("No volumes. Create one here, or run a container with -v NAME:/path.")
	} else {
		v.Message.Set("")
	}
	if _, ok := v.Find(v.Selected.Get()); !ok {
		v.Selected.Set("")
	}
}

// Find returns the row of the volume name.
func (v *Volumes) Find(name string) (VolumeRow, bool) {
	for i := 0; i < v.Rows.Len(); i++ {
		if r := v.Rows.At(i); r.Name == name {
			return r, true
		}
	}
	return VolumeRow{}, false
}

func (v *Volumes) inspect() {
	name := v.Selected.Get()
	if name == "" {
		v.Inspect.Clear()
		return
	}
	go func() {
		raw, err := v.app.eng.VolumeInspect(context.Background(), name)
		v.app.post(func() {
			if name != v.Selected.Get() {
				return
			}
			tree, terr := InspectTree(raw)
			switch {
			case err != nil:
				tree = []InspectNode{{Key: "error", Value: Clean(err.Error(), maxField)}}
			case terr != nil:
				tree = []InspectNode{{Key: "error", Value: Clean(terr.Error(), maxField)}}
			}
			replace(v.Inspect, tree)
		})
	}()
}

// CreateLine is the create field's command line.
func (v *Volumes) CreateLine() string {
	if n := strings.TrimSpace(v.NewName.Get()); n != "" {
		return Line("volume", "create", n)
	}
	return Line("volume", "create")
}

// Create creates a volume named NewName (the engine names it when empty).
func (v *Volumes) Create() {
	name := strings.TrimSpace(v.NewName.Get())
	v.app.Do(v.CreateLine(), func(ctx context.Context) error {
		_, err := v.app.eng.VolumeCreate(ctx, api.VolumeCreateOptions{Name: name})
		if err == nil {
			v.app.post(func() { v.NewName.Set("") })
		}
		return err
	})
}

// RemoveLine is the command line of Remove.
func (v *Volumes) RemoveLine(name string, force bool) string {
	if force {
		return Line("volume", "rm", "-f", name)
	}
	return Line("volume", "rm", name)
}

// Remove removes a volume.
func (v *Volumes) Remove(name string, force bool) {
	v.app.Do(v.RemoveLine(name, force), func(ctx context.Context) error {
		return v.app.eng.VolumeRemove(ctx, name, force)
	})
}

// PruneLine is the command line of Prune.
func (v *Volumes) PruneLine(all bool) string {
	if all {
		return Line("volume", "prune", "-a", "-f")
	}
	return Line("volume", "prune", "-f")
}

// Prune removes unused volumes (anonymous ones, or with all every unused
// one) and says what it reclaimed.
func (v *Volumes) Prune(all bool) {
	v.app.Do(v.PruneLine(all), func(ctx context.Context) error {
		res, err := v.app.eng.VolumesPrune(ctx, all)
		if err == nil {
			msg := fmt.Sprintf("pruned %d volume(s), %s reclaimed", len(res.VolumesDeleted), Size(res.SpaceReclaimed))
			v.app.post(func() { v.Notice.Set(msg) })
		}
		return err
	})
}
