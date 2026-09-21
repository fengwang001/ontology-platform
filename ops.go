package ontology

import "math"

// OpStats reports instrumentation about one compressed-domain set operation.
type OpStats struct {
	// Steps is the number of merge-loop iterations, i.e. how many run
	// segments the two cursors advanced through. It is bounded by the
	// total number of runs of the inputs, never by the number of bits.
	Steps int
}

// Union returns the set union of b and o, computed directly on the run
// representation without expanding to a per-bit form.
func (b *Bitmap) Union(o *Bitmap) (*Bitmap, OpStats) {
	return mergeRuns(b, o, func(x, y bool) bool { return x || y })
}

// Intersect returns the set intersection of b and o, computed directly on
// the run representation without expanding to a per-bit form.
func (b *Bitmap) Intersect(o *Bitmap) (*Bitmap, OpStats) {
	return mergeRuns(b, o, func(x, y bool) bool { return x && y })
}

// Difference returns the set difference b \ o, computed directly on the
// run representation without expanding to a per-bit form.
func (b *Bitmap) Difference(o *Bitmap) (*Bitmap, OpStats) {
	return mergeRuns(b, o, func(x, y bool) bool { return x && !y })
}

// snapshotRuns copies the run list and prefix sums under a read lock so
// the merge can proceed without holding any lock.
func (b *Bitmap) snapshotRuns() ([]run, []uint64) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	r := make([]run, len(b.runs))
	copy(r, b.runs)
	e := make([]uint64, len(b.ends))
	copy(e, b.ends)
	return r, e
}

// mergeRuns is the two-cursor compressed-domain merge behind every set
// operation. It walks both run lists in lockstep, emitting one output
// segment per maximal interval on which both inputs are constant.
func mergeRuns(x, y *Bitmap, op func(a, b bool) bool) (*Bitmap, OpStats) {
	xr, xe := x.snapshotRuns()
	yr, ye := y.snapshotRuns()
	out := make([]run, 0, len(xr)+len(yr))
	var st OpStats
	var i, j int
	var pos uint64
	for {
		for i < len(xe) && xe[i] <= pos {
			i++
		}
		for j < len(ye) && ye[j] <= pos {
			j++
		}
		if i == len(xe) && j == len(ye) {
			break // both exhausted: the rest is zeros under any op
		}
		var av, bv bool
		next := uint64(math.MaxUint64)
		if i < len(xe) {
			av = xr[i].one
			next = min(next, xe[i])
		}
		if j < len(ye) {
			bv = yr[j].one
			next = min(next, ye[j])
		}
		v := op(av, bv)
		if n := len(out); n > 0 && out[n-1].one == v {
			out[n-1].length += next - pos
		} else {
			out = append(out, run{one: v, length: next - pos})
		}
		pos = next
		st.Steps++
	}
	res := &Bitmap{runs: out}
	res.normalizeLocked() // strips any trailing zero run, rebuilds ends
	return res, st
}
