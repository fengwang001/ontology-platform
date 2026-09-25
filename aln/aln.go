// Package aln tracks per-batch aligned times: batch open/close, the running
// minimum TS inside the open batch, monotonicity and lateness decisions.
// It depends on no other package of this module.
package aln

import "math"

// None is the aligned time of a batch with no accepted events when no earlier
// batch produced one either: "none" (infinity, i.e. no lower bound at all).
const None = int64(math.MaxInt64)

// Aligner maintains aligned times incrementally. It is not safe for
// concurrent use; callers (package api) serialize access.
type Aligner struct {
	times   []int64 // finalized aligned time per closed batch
	prev    int64   // aligned time of the previous (last closed) batch
	hasPrev bool
	curMin  int64 // running min TS of accepted events in the open batch
	hasCur  bool
	count   int  // accepted events in the open batch
	open    bool // a batch is currently open
	cmp     int  // comparisons spent maintaining curMin (incremental proof)
}

// Open reports whether a batch is currently open.
func (a *Aligner) Open() bool { return a.open }

// Begin opens a new batch, closing the current one first if any.
func (a *Aligner) Begin() {
	if a.open {
		a.Close()
	}
	a.open, a.hasCur, a.count = true, false, 0
}

// Close finalizes the open batch; it is a no-op when no batch is open.
// The aligned time is the min TS of accepted events, carried over from the
// previous batch when the closing batch accepted none.
func (a *Aligner) Close() {
	if !a.open {
		return
	}
	if a.hasCur {
		a.prev, a.hasPrev = a.curMin, true
	}
	a.open = false
	if a.hasPrev {
		a.times = append(a.times, a.prev)
	} else {
		a.times = append(a.times, None)
	}
}

// Late reports whether ts is strictly below the previous batch's aligned
// time. With no previous aligned time nothing is late.
func (a *Aligner) Late(ts int64) bool { return a.hasPrev && ts < a.prev }

// Count returns the accepted-event count of the open batch.
func (a *Aligner) Count() int { return a.count }

// Accept records one accepted event, maintaining the running min with
// exactly one comparison per event (never a full batch rescan).
func (a *Aligner) Accept(ts int64) {
	a.cmp++
	if !a.hasCur || ts < a.curMin {
		a.curMin = ts
	}
	a.hasCur = true
	a.count++
}

// Times returns a copy of the finalized aligned times, one per closed batch.
func (a *Aligner) Times() []int64 { return append([]int64(nil), a.times...) }
