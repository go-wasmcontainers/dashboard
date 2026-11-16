package ui

import (
	"github.com/go-widgets/mvvm"
	"github.com/go-widgets/toolkit"

	"github.com/go-wasmcontainers/dashboard/vm"
)

// pullSet copies a table's multi-row selection into the view model's set,
// as the keys of the rows it holds. The toolkit has no event for a change
// of that set, so the scene pulls it after every input that can change it.
func pullSet[R any](t *toolkit.Table, rows *mvvm.ObservableList[R], key func(R) string, set *mvvm.Observable[[]string]) {
	var keys []string
	for _, i := range t.SelectedRows() {
		if i >= 0 && i < rows.Len() {
			keys = append(keys, key(rows.At(i)))
		}
	}
	set.Set(keys)
}

// pullSelection drives every view model's selected set from its table.
func (s *Scene) pullSelection() {
	a := s.app
	pullSet(s.cTable, a.Containers.Rows, func(r vm.ContainerRow) string { return r.ID }, a.Containers.SelectedSet)
	pullSet(s.iTable, a.Images.Rows, vm.ImageRow.Key, a.Images.SelectedSet)
	pullSet(s.vTable, a.Volumes.Rows, func(r vm.VolumeRow) string { return r.Name }, a.Volumes.SelectedSet)
	pullSet(s.nTable, a.Networks.Rows, func(r vm.NetworkRow) string { return r.Name }, a.Networks.SelectedSet)
}
