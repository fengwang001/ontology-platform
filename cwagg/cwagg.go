// Package cwagg keeps per-key tumbling-window aggregates: accepted-element
// counts and sums per window, the watermark, window closing, fired outputs
// and the dropped counter. It depends only on cntwin and is not safe for
// concurrent use; callers must serialize access.
package cwagg

import "ontology/cntwin"

// Event is one upstream change.
type Event struct {
	Key string
	Pos int64
	Val int64
}

// Fire is the aggregate emitted once when a window closes.
type Fire struct {
	Key string
	Win int64
	Sum int64
}

type window struct {
	cnt, sum int64
	closed   bool
}

type keyState struct {
	wm   int64
	wins map[int64]*window
	seen map[int64]struct{} // delivered positions, for idempotent duplicates
}

// Agg aggregates events per key into count-based tumbling windows.
type Agg struct {
	size, lateness int64
	keys           map[string]*keyState
	fired          []Fire
	dropped        int64
	checked        int64 // windows inspected for the trigger test on the last Add
}

// New returns an empty Agg. Callers must guarantee size > 0, lateness >= 0.
func New(size, lateness int64) *Agg {
	return &Agg{size: size, lateness: lateness, keys: map[string]*keyState{}}
}

// Add applies one validated event (Key non-empty, Pos >= 0).
func (a *Agg) Add(e Event) {
	a.checked = 0
	ks := a.keys[e.Key]
	if ks == nil {
		ks = &keyState{wm: -1, wins: map[int64]*window{}, seen: map[int64]struct{}{}}
		a.keys[e.Key] = ks
	}
	if _, dup := ks.seen[e.Pos]; dup {
		return // idempotent: no count, no watermark advance
	}
	ks.seen[e.Pos] = struct{}{}

	a.checked = 1 // exactly one window is located, via floor(Pos/size)
	k := cntwin.Window(e.Pos, a.size)
	w := ks.wins[k]
	accept := true
	switch {
	case w != nil && w.closed:
		accept = false // a fired window never accepts again
	case cntwin.Late(e.Pos, ks.wm) && !cntwin.Acceptable(e.Pos, ks.wm, a.lateness):
		accept = false // late beyond the allowed lateness
	}
	if !accept {
		a.dropped++
	} else {
		if w == nil {
			w = &window{}
			ks.wins[k] = w
		}
		w.cnt++
		w.sum += e.Val
		if cntwin.Triggered(w.cnt, a.size) {
			w.closed = true
			a.fired = append(a.fired, Fire{Key: e.Key, Win: k, Sum: w.sum})
		}
	}
	if e.Pos > ks.wm { // accepted or dropped, the watermark always advances
		ks.wm = e.Pos
	}
}

// Fired returns a copy of all fires so far, in emission order.
func (a *Agg) Fired() []Fire { return append([]Fire(nil), a.fired...) }

// Dropped returns the number of discarded elements.
func (a *Agg) Dropped() int64 { return a.dropped }
