// Package res accounts operator resources (opens/closes/spill files) and
// provides lifecycle helpers and leak assertions.
package res

import "sync/atomic"

// Tracker is a concurrency-safe resource counter shared across operator
// trees. Counters are unexported; tests read them through accessors.
type Tracker struct {
	opens      atomic.Int64
	closes     atomic.Int64
	spillOpen  atomic.Int64
	spillClose atomic.Int64
	spillPeak  atomic.Int64
}

// NewTracker returns an empty tracker.
func NewTracker() *Tracker { return &Tracker{} }

// Opened records one successful operator Open.
func (t *Tracker) Opened() {
	if t == nil {
		return
	}
	t.opens.Add(1)
}

// Closed records one actual operator Close.
func (t *Tracker) Closed() {
	if t == nil {
		return
	}
	t.closes.Add(1)
}

// SpillCreated records a new spill file and updates the live peak.
func (t *Tracker) SpillCreated() {
	if t == nil {
		return
	}
	n := t.spillOpen.Add(1)
	for {
		p := t.spillPeak.Load()
		if n <= p || t.spillPeak.CompareAndSwap(p, n) {
			break
		}
	}
}

// SpillRemoved records deletion of one spill file.
func (t *Tracker) SpillRemoved() {
	if t == nil {
		return
	}
	t.spillClose.Add(1)
}

// Opens returns the number of successful Open calls.
func (t *Tracker) Opens() int64 {
	if t == nil {
		return 0
	}
	return t.opens.Load()
}

// Closes returns the number of actual Close calls.
func (t *Tracker) Closes() int64 {
	if t == nil {
		return 0
	}
	return t.closes.Load()
}

// LiveSpills returns spill files created but not yet removed.
func (t *Tracker) LiveSpills() int64 {
	if t == nil {
		return 0
	}
	return t.spillOpen.Load() - t.spillClose.Load()
}

// SpillPeak returns the high-water mark of concurrently live spill files.
func (t *Tracker) SpillPeak() int64 {
	if t == nil {
		return 0
	}
	return t.spillPeak.Load()
}

// Balanced reports opens==closes and no live spill files.
func (t *Tracker) Balanced() bool {
	return t.Opens() == t.Closes() && t.LiveSpills() == 0
}
