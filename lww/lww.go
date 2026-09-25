// Package lww maintains global last-write-wins final values per key
// (largest accepted TS wins; ties resolved in arrival order, later
// wins) together with the dropped-event count. It depends only on aln.
package lww

import "ontology/aln"

// Event is one upstream change.
type Event struct {
	Key string
	TS  int64
	V   int
}

type entry struct {
	ts int64
	v  int
}

// Register is the global LWW state. Construct with New.
type Register struct {
	a       *aln.Aligner
	vals    map[string]entry
	dropped int
}

// New returns an empty register backed by a fresh aligner.
func New() *Register {
	return &Register{a: aln.New(), vals: make(map[string]entry)}
}

// Aligner exposes the backing aligner (used by the api layer for
// batch-state and capacity pre-checks).
func (r *Register) Aligner() *aln.Aligner { return r.a }

// Begin opens the next batch.
func (r *Register) Begin() { r.a.Begin() }

// Feed applies one event. A late event is counted as dropped and
// changes neither any value nor the batch aligned time; an accepted
// event folds into the running batch minimum (via aln) and the global
// LWW value. LWW uses ts >= current so that, on equal TS, the later
// arrival wins. Applied online this is independent of arrival order
// whenever TS differ; only exact ties depend on arrival order, which is
// exactly the required "align within batch, global LWW" semantics.
func (r *Register) Feed(ev Event) (accepted bool) {
	if !r.a.Accept(ev.TS) {
		r.dropped++
		return false
	}
	c, ok := r.vals[ev.Key]
	if !ok || ev.TS >= c.ts {
		r.vals[ev.Key] = entry{ev.TS, ev.V}
	}
	return true
}

// Close finalizes the open batch.
func (r *Register) Close() { r.a.Close() }

// Value returns the current final value for key and whether it exists.
func (r *Register) Value(key string) (int, bool) {
	e, ok := r.vals[key]
	return e.v, ok
}

// Dropped returns the number of late, dropped events.
func (r *Register) Dropped() int { return r.dropped }

// Aligned returns a copy of the finalized batches' aligned times.
func (r *Register) Aligned() []int64 { return r.a.Aligned() }
