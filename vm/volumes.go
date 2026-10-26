package vm

import (
	"errors"
	"sort"

	"github.com/go-widgets/mvvm"

	"github.com/go-wasmcontainers/dashboard/engine"
)

// VolumeRow is one volume.
type VolumeRow struct {
	Name, Driver, Mountpoint, Created, Size string
}

// VolumeColumns are the column titles.
var VolumeColumns = []string{"Name", "Driver", "Mountpoint", "Created", "Size"}

// Cells is the row as table cells.
func (r VolumeRow) Cells() []string {
	return []string{r.Name, r.Driver, r.Mountpoint, r.Created, r.Size}
}

// Volume support, as the engine answered.
const (
	VolumesUnknown = iota
	VolumesUnsupported
	VolumesSupported
)

// Volumes is the volumes view. Until the engine serves GET /volumes it
// shows what it needs, never invented rows.
type Volumes struct {
	app       *App
	Rows      *mvvm.ObservableList[VolumeRow]
	Supported *mvvm.Observable[int]
	// Message explains an empty or unavailable view.
	Message *mvvm.Observable[string]
}

func newVolumes(a *App) *Volumes {
	return &Volumes{app: a, Rows: mvvm.NewObservableList[VolumeRow](),
		Supported: mvvm.NewObservable(VolumesUnknown), Message: mvvm.NewObservable("")}
}

func (v *Volumes) apply(list []engine.Volume, err error, version string) {
	switch {
	case errors.Is(err, engine.ErrUnsupported):
		v.Supported.Set(VolumesUnsupported)
		v.Message.Set("Volumes require an engine that serves GET /volumes; engine " + Clean(version, 40) +
			" does not yet. This view fills in by itself once it does.")
		v.Rows.Clear()
		return
	case err != nil:
		v.Message.Set("volumes: " + Clean(err.Error(), maxField))
		return
	}
	v.Supported.Set(VolumesSupported)
	rows := make([]VolumeRow, 0, len(list))
	for _, x := range list {
		size := ""
		if x.UsageData != nil && x.UsageData.Size >= 0 {
			size = Size(x.UsageData.Size)
		}
		rows = append(rows, VolumeRow{Name: Clean(x.Name, maxField), Driver: Clean(x.Driver, 64),
			Mountpoint: Clean(x.Mountpoint, maxField), Created: Clean(x.CreatedAt, 64), Size: size})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	Sync(v.Rows, rows, func(r VolumeRow) string { return r.Name })
	if len(rows) == 0 {
		v.Message.Set("No volumes.")
	} else {
		v.Message.Set("")
	}
}
