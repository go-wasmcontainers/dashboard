package vm

import (
	"fmt"
	"sort"
	"time"

	"github.com/go-widgets/mvvm"
)

// Frame is one presented image of a GPU surface: an ImageBitmap in the
// browser. Whoever holds it closes it, exactly once.
type Frame interface{ Close() }

// Surface limits: the size a surface opens at when it says none, the largest
// one accepted, how many panes there can be, and how many presentation times
// the frame-rate window keeps.
const (
	DefaultSurfaceW, DefaultSurfaceH = 640, 480
	MaxSurfaceW, MaxSurfaceH         = 1920, 1080
	MaxSurfaces                      = 16
	maxTimes                         = 120
)

// SurfaceRow is one surface pane.
type SurfaceRow struct {
	ID     uint32
	Title  string
	Status string
	W, H   int
	// Closed: the guest closed the surface; the pane keeps its last frame,
	// greyed, until it is dismissed.
	Closed bool
	// Shown: a frame has been presented (the pane has an image to draw).
	Shown bool
}

type surface struct {
	row        SurfaceRow
	container  string
	pid        uint32
	pending    Frame
	pendingSeq uint64
	pw, ph     int
	lastSeq    uint64
	times      []time.Time
	move       *SurfaceInput // the pointer move waiting for this frame
}

// Surfaces holds the GPU surfaces the engine reports: the panes, and the
// newest frame of each until it is presented. Every frame it is given is
// closed and acknowledged exactly once: presented, replaced by a newer one,
// for a surface that is unknown or closed, or out of order. The engine
// paces its guest on those acknowledgements, so none may be lost.
type Surfaces struct {
	app   *App
	ack   func(surface uint32, seq uint64)
	input func(SurfaceInput)
	byID  map[uint32]*surface
	// Rows are the panes, by surface ID.
	Rows *mvvm.ObservableList[SurfaceRow]
}

func newSurfaces(a *App) *Surfaces {
	return &Surfaces{app: a, byID: map[uint32]*surface{}, Rows: mvvm.NewObservableList[SurfaceRow]()}
}

// SetAck sets how a frame is acknowledged to the engine ({t:"frame-ack"}).
func (s *Surfaces) SetAck(ack func(surface uint32, seq uint64)) { s.ack = ack }

func (s *Surfaces) acknowledge(id uint32, seq uint64) {
	if s.ack != nil {
		s.ack(id, seq)
	}
}

// size bounds a reported size: unset or nonsense opens at the default, too
// large is clamped.
func size(w, h int) (int, int) {
	if w <= 0 || h <= 0 {
		return DefaultSurfaceW, DefaultSurfaceH
	}
	return min(w, MaxSurfaceW), min(h, MaxSurfaceH)
}

// Open adds the pane of a surface (or reopens one with the same ID).
func (s *Surfaces) Open(id uint32, container string, pid uint32, w, h int) {
	sf, ok := s.byID[id]
	if !ok {
		if len(s.byID) >= MaxSurfaces {
			s.app.Error.Set(fmt.Sprintf("surface %d not shown: at most %d surface panes", id, MaxSurfaces))
			return
		}
		sf = &surface{}
		s.byID[id] = sf
	}
	s.drop(sf)
	w, h = size(w, h)
	*sf = surface{container: Clean(container, 64), pid: pid, row: SurfaceRow{ID: id, W: w, H: h}}
	s.publish()
}

// Resize changes a surface's size.
func (s *Surfaces) Resize(id uint32, w, h int) {
	if sf, ok := s.byID[id]; ok && !sf.row.Closed {
		sf.row.W, sf.row.H = size(w, h)
		s.publish()
	}
}

// Close marks a surface closed; its pane keeps the last frame, greyed.
func (s *Surfaces) Close(id uint32) {
	if sf, ok := s.byID[id]; ok {
		s.drop(sf)
		sf.row.Closed = true
		s.publish()
	}
}

// Dismiss removes a pane, and reports whether there was one (the view then
// forgets its image).
func (s *Surfaces) Dismiss(id uint32) bool {
	sf, ok := s.byID[id]
	if !ok {
		return false
	}
	s.drop(sf)
	delete(s.byID, id)
	s.publish()
	return true
}

// drop closes and acknowledges the frame a surface holds, if any.
func (s *Surfaces) drop(sf *surface) {
	if sf.pending != nil {
		sf.pending.Close()
		s.acknowledge(sf.row.ID, sf.pendingSeq)
		sf.pending = nil
	}
}

// Frame takes frame seq of surface id. Only the newest is kept: an older
// one still waiting is closed and acknowledged as dropped, and so is a frame
// for a surface that is unknown or closed, or one that does not come after
// the last.
func (s *Surfaces) Frame(id uint32, seq uint64, w, h int, f Frame) {
	sf, ok := s.byID[id]
	if !ok || sf.row.Closed || seq <= sf.lastSeq {
		f.Close()
		s.acknowledge(id, seq)
		return
	}
	s.drop(sf)
	sf.pending, sf.pendingSeq, sf.lastSeq = f, seq, seq
	sf.pw, sf.ph = size(w, h)
}

// Take hands over the frame waiting for surface id, with its size; the
// caller draws it, closes it, and calls Presented.
func (s *Surfaces) Take(id uint32) (f Frame, seq uint64, w, h int, ok bool) {
	sf, found := s.byID[id]
	if !found || sf.pending == nil {
		return nil, 0, 0, 0, false
	}
	f, seq, w, h = sf.pending, sf.pendingSeq, sf.pw, sf.ph
	sf.pending = nil
	return f, seq, w, h, true
}

// Presented acknowledges frame seq of surface id, drawn, and counts it in
// the frame rate.
func (s *Surfaces) Presented(id uint32, seq uint64, w, h int) {
	s.acknowledge(id, seq)
	sf, ok := s.byID[id]
	if !ok {
		return
	}
	now := s.app.now()
	sf.times = append(sf.times, now)
	cut := 0
	for cut < len(sf.times) && now.Sub(sf.times[cut]) >= time.Second {
		cut++
	}
	if len(sf.times)-cut > maxTimes {
		cut = len(sf.times) - maxTimes
	}
	sf.times = append(sf.times[:0], sf.times[cut:]...)
	sf.row.W, sf.row.H, sf.row.Shown = w, h, true
	s.publish()
}

// FPS is how many frames were presented in the last second.
func (s *Surfaces) FPS(id uint32) int {
	sf, ok := s.byID[id]
	if !ok {
		return 0
	}
	n := 0
	now := s.app.now()
	for _, t := range sf.times {
		if now.Sub(t) < time.Second {
			n++
		}
	}
	return n
}

// Has reports whether surface id has a pane (whatever the rows show while
// they are being rewritten).
func (s *Surfaces) Has(id uint32) bool {
	_, ok := s.byID[id]
	return ok
}

// Find returns the pane of surface id.
func (s *Surfaces) Find(id uint32) (SurfaceRow, bool) {
	for i := 0; i < s.Rows.Len(); i++ {
		if r := s.Rows.At(i); r.ID == id {
			return r, true
		}
	}
	return SurfaceRow{}, false
}

// IDs are the surfaces with a pane, in order.
func (s *Surfaces) IDs() []uint32 {
	ids := make([]uint32, 0, len(s.byID))
	for id := range s.byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// owner names the container of a surface: its name when the engine says
// which container it is (and the dashboard knows it), else its ID, else
// the process.
func (s *Surfaces) owner(sf *surface) string {
	if sf.container == "" {
		return fmt.Sprintf("pid %d", sf.pid)
	}
	for i := 0; i < s.app.Containers.Rows.Len(); i++ {
		if r := s.app.Containers.Rows.At(i); r.ID == sf.container || ShortID(r.ID) == ShortID(sf.container) {
			return r.Name
		}
	}
	return ShortID(sf.container)
}

// publish rewrites the rows from the surfaces.
func (s *Surfaces) publish() {
	rows := make([]SurfaceRow, 0, len(s.byID))
	for _, id := range s.IDs() {
		sf := s.byID[id]
		r := sf.row
		r.Title = fmt.Sprintf("%s — surface %d", s.owner(sf), id)
		switch {
		case r.Closed:
			r.Status = fmt.Sprintf("%dx%d, closed", r.W, r.H)
		case !r.Shown:
			r.Status = fmt.Sprintf("%dx%d, waiting for first frame", r.W, r.H)
		default:
			r.Status = fmt.Sprintf("%dx%d, %d fps", r.W, r.H, s.FPS(id))
		}
		rows = append(rows, r)
	}
	replace(s.Rows, rows)
}
