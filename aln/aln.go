// Package aln tracks per-batch aligned time: batch index, the running
// minimum TS of accepted events in the open batch, monotonicity and
// lateness decisions. It depends on no other package.
package aln

import "math"

// None means "no aligned time yet / no lower bound" (batch 0 with no
// accepted event). It is +infinity, so every finite TS is >= None is
// false and every finite TS is treated as on time against it.
const None = int64(math.MaxInt64)

// Aligner is a batch aligned-time tracker. Construct with New.
type Aligner struct {
	open     bool    // a batch is currently open
	prev     int64   // aligned time of the previous finalized batch
	curMin   int64   // running minimum TS of accepted events in open batch
	curCount int     // accepted-event count in the open batch
	aligned  []int64 // finalized batches, in opening order
	// cmps counts, per fed-and-accepted event, the comparisons made to
	// maintain the running minimum. Unexported: never read via the public
	// API; only same-package tests may inspect it.
	cmps int
}

// New returns an aligner with no open batch.
func New() *Aligner { return &Aligner{prev: None, curMin: None} }

// Begin closes any open batch and opens the next one; batch numbers
// start at 0 and follow opening order.
func (a *Aligner) Begin() {
	if a.open {
		a.Close()
	}
	a.open = true
	a.curMin = None
	a.curCount = 0
}

// WouldAccept reports whether Accept(ts) would accept ts, without
// mutating any state. An event is late (rejected) iff the previous
// batch has a finite aligned time and ts is strictly below it; None
// means no lower bound, so nothing is late against batch 0.
func (a *Aligner) WouldAccept(ts int64) bool {
	return a.open && (a.prev == None || ts >= a.prev)
}

// Accept folds ts into the open batch when it is not late. It returns
// false (and changes nothing) when there is no open batch or the event
// is late. Each accepted event is compared exactly once against the
// running minimum, so the minimum is maintained incrementally.
func (a *Aligner) Accept(ts int64) bool {
	if !a.open {
		return false
	}
	if a.prev != None && ts < a.prev {
		return false
	}
	a.cmps++
	a.curCount++
	if ts < a.curMin {
		a.curMin = ts
	}
	return true
}

// Count is the number of accepted events in the open batch.
func (a *Aligner) Count() int { return a.curCount }

// Open reports whether a batch is currently open.
func (a *Aligner) Open() bool { return a.open }

// Close finalizes the open batch: its aligned time is the running
// minimum TS, or the previous aligned time when the batch accepted no
// events. It is a no-op without an open batch.
func (a *Aligner) Close() {
	if !a.open {
		return
	}
	t := a.prev
	if a.curCount > 0 {
		t = a.curMin
	}
	a.aligned = append(a.aligned, t)
	a.prev = t
	a.open = false
}

// Aligned returns a copy of the finalized batches' aligned times.
func (a *Aligner) Aligned() []int64 {
	out := make([]int64, len(a.aligned))
	copy(out, a.aligned)
	return out
}
