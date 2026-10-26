package ui

import (
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"

	"github.com/go-wasmcontainers/dashboard/vm"
)

// statsPanel is the detail's Stats tab: a memory gauge (share of the
// limit), a sparkline of the last readings, and the counters as text. CPU
// shows "--" with the engine's reason: a wasm guest's CPU is not measured,
// and 0% would read as idle.
type statsPanel struct {
	toolkit.Base
	gauge                      *toolkit.Gauge
	spark                      *toolkit.Sparkline
	mem, cpu, counters, header *toolkit.Label
}

func newStatsPanel(d *vm.Detail, inv func()) *statsPanel {
	p := &statsPanel{gauge: toolkit.NewGauge(0, 100, 0), spark: toolkit.NewSparkline(nil),
		mem: toolkit.NewLabel(""), cpu: toolkit.NewLabel(""), counters: toolkit.NewLabel(""), header: toolkit.NewLabel("")}
	p.gauge.Caption = "memory"
	p.gauge.Bands = []toolkit.GaugeBand{{Upto: 70, Color: toolkit.RGB(0x2E, 0x9E, 0x5B)},
		{Upto: 90, Color: toolkit.RGB(0xD0, 0x8C, 0x1C)}, {Upto: 100, Color: toolkit.RGB(0xC6, 0x3B, 0x3B)}}
	p.spark.ShowLast = true
	d.Memory.Subscribe(func(f float64) {
		p.gauge.Value().Set(max(f, 0) * 100)
		inv()
	})
	d.MemHistory.SubscribeChanged(func() {
		p.spark.Values = d.MemHistory.Slice()
		inv()
	})
	bindLabel(p.mem, func() string {
		if d.MemoryText.Get() == "" {
			return "memory: no reading yet"
		}
		return "memory " + d.MemoryText.Get()
	}, inv, d.MemoryText)
	bindLabel(p.cpu, func() string {
		if n := d.CPUNote.Get(); n != "" {
			return "CPU " + d.CPU.Get() + " (" + n + ")"
		}
		return "CPU " + d.CPU.Get()
	}, inv, d.CPU, d.CPUNote)
	bindLabel(p.counters, d.Counters.Get, inv, d.Counters)
	bindLabel(p.header, func() string { return "= " + d.StatsLine.Get() }, inv, d.StatsLine)
	return p
}

// SetBounds lays the panel out: the gauge on the left, the rest beside it.
func (p *statsPanel) SetBounds(r toolkit.Rect) {
	p.Base.SetBounds(r)
	g := min(r.H-8, 140)
	p.gauge.SetBounds(toolkit.Rect{X: r.X + 4, Y: r.Y + 4, W: max(g, 1), H: max(g, 1)})
	x := r.X + g + 16
	w := max(r.W-g-20, 1)
	y := r.Y + 4
	for _, l := range []*toolkit.Label{p.header, p.mem, p.cpu, p.counters} {
		l.SetBounds(toolkit.Rect{X: x, Y: y, W: w, H: 20})
		y += 22
	}
	p.spark.SetBounds(toolkit.Rect{X: x, Y: y + 4, W: w, H: max(r.Y+r.H-y-8, 1)})
}

// Draw paints the children.
func (p *statsPanel) Draw(pt painter.Painter, theme *toolkit.Theme) {
	for _, w := range []toolkit.Widget{p.gauge, p.header, p.mem, p.cpu, p.counters, p.spark} {
		w.Draw(pt, theme)
	}
}
