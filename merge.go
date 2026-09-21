package ontology

// combineOp combines the bit values of two sets at one position.
type combineOp func(a, b bool) bool

// Union returns the set of bits present in either s or other.
func (s *Set) Union(other *Set) *Set {
	return combine(s, other, func(a, b bool) bool { return a || b })
}

// Intersect returns the set of bits present in both s and other.
func (s *Set) Intersect(other *Set) *Set {
	return combine(s, other, func(a, b bool) bool { return a && b })
}

// Difference returns the set of bits present in s but not in other.
func (s *Set) Difference(other *Set) *Set {
	return combine(s, other, func(a, b bool) bool { return a && !b })
}

// LastMergeSteps reports how many run-advance steps the merge that
// produced this set took. It is O(runs), never O(bits).
func (s *Set) LastMergeSteps() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mergeSteps
}

// combine merges two sets on the compressed run representation. It takes
// consistent snapshots under each set's read lock (never holding both
// locks at once, so no lock ordering is needed), then walks two cursors
// over the run lists. Bits are never expanded.
func combine(a, b *Set, op combineOp) *Set {
	if a == b {
		// Trivial cases that avoid self-referential merging.
		res := &Set{runs: a.snapshot(), mergeSteps: 1}
		if isDifference(op) {
			res.runs = nil
		}
		return res
	}
	runs, steps := mergeRuns(a.snapshot(), b.snapshot(), op)
	return &Set{runs: runs, mergeSteps: steps}
}

// isDifference reports whether op is the difference truth table.
func isDifference(op combineOp) bool {
	return op(true, false) && !op(true, true) && !op(false, true) && !op(false, false)
}

// mergeRuns walks the two run lists with a cursor each. At every step it
// advances by the smaller remaining run length, emits the combined value
// for that span (merging into the previous output run when equal), and
// counts one step. Runs in O(len(a)+len(b)) steps.
func mergeRuns(a, b []run, op combineOp) ([]run, int) {
	var out []run
	var remA, remB uint64
	valA, valB := false, false
	ia, ib := 0, 0
	steps := 0
	for remA > 0 || ia < len(a) || remB > 0 || ib < len(b) {
		if remA == 0 && ia < len(a) {
			valA, remA = a[ia].val == 1, a[ia].length
			ia++
		}
		if remB == 0 && ib < len(b) {
			valB, remB = b[ib].val == 1, b[ib].length
			ib++
		}
		// An exhausted side reads as zero forever.
		if remA == 0 {
			valA = false
		}
		if remB == 0 {
			valB = false
		}
		var d uint64
		switch {
		case remA == 0:
			d = remB
		case remB == 0:
			d = remA
		default:
			d = min(remA, remB)
		}
		emit(&out, op(valA, valB), d)
		if remA > 0 {
			remA -= d
		}
		if remB > 0 {
			remB -= d
		}
		steps++
	}
	// Drop the trailing all-zero run to keep the canonical form.
	for len(out) > 0 && out[len(out)-1].val == 0 {
		out = out[:len(out)-1]
	}
	return out, steps
}

// emit appends a span of length d with value v, merging with the
// previous run when the value matches.
func emit(out *[]run, v bool, d uint64) {
	var val uint8
	if v {
		val = 1
	}
	if n := len(*out); n > 0 && (*out)[n-1].val == val {
		(*out)[n-1].length += d
		return
	}
	*out = append(*out, run{val, d})
}
