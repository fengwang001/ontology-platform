package ontology

import "sync"

// run is one maximal block of equal-valued bits. length is uint64 so a
// single run can cover the whole 2^32 universe.
type run struct {
	val    uint8 // 0 or 1
	length uint64
}

// Set is a run-length encoded bitmap set over the uint32 universe.
// Invariants (checked by Verify):
//  1. runs alternate in value; adjacent same-value runs are merged
//  2. no zero-length runs
//  3. no trailing zero-value run (bits past the last run are all zero)
type Set struct {
	mu         sync.RWMutex
	runs       []run
	mergeSteps int // runs advanced by the merge that produced this set
	statRuns   int // runs visited by the most recent Count/Min/Max call
}

// New returns an empty set.
func New() *Set { return &Set{} }

// normalize merges adjacent same-value runs, drops zero-length runs and
// strips trailing zero-value runs. It is the single place that restores
// the canonical form after any mutation.
func normalize(runs []run) []run {
	out := make([]run, 0, len(runs))
	for _, r := range runs {
		if r.length == 0 {
			continue
		}
		if n := len(out); n > 0 && out[n-1].val == r.val {
			out[n-1].length += r.length
			continue
		}
		out = append(out, r)
	}
	for len(out) > 0 && out[len(out)-1].val == 0 {
		out = out[:len(out)-1]
	}
	return out
}

// Verify reports whether the run list satisfies all three normalization
// invariants: no zero-length runs, no adjacent same-value runs, and no
// trailing zero-value run.
func (s *Set) Verify() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i, r := range s.runs {
		if r.length == 0 || r.val > 1 {
			return false
		}
		if i > 0 && s.runs[i-1].val == r.val {
			return false
		}
	}
	if n := len(s.runs); n > 0 && s.runs[n-1].val == 0 {
		return false
	}
	return true
}

// Runs returns the number of runs in the compressed representation.
func (s *Set) Runs() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.runs)
}

// snapshot returns a copy of the run list under the read lock.
func (s *Set) snapshot() []run {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]run(nil), s.runs...)
}
