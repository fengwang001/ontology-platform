package docsync

import "sync/atomic"

// counters instrument node visits so tests can prove work is sub-linear in
// both total line count and total diagnostic count.
type counters struct {
	linesVisited atomic.Int64
	diagsVisited atomic.Int64
}

func (c *counters) line() { c.linesVisited.Add(1) }
func (c *counters) diag() { c.diagsVisited.Add(1) }

func (st *Store) ResetCounters() {
	st.counters.linesVisited.Store(0)
	st.counters.diagsVisited.Store(0)
}

func (st *Store) LineVisits() int64 { return st.counters.linesVisited.Load() }
func (st *Store) DiagVisits() int64 { return st.counters.diagsVisited.Load() }
