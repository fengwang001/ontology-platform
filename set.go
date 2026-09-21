// Package ontology implements a run-length-encoded (RLE) bitmap set over
// the full uint32 domain. The set is stored as a sorted list of inclusive
// intervals ("runs") of consecutive set bits; zero-runs are never stored.
//
// Canonical form invariants (enforced by every mutator, checked by Verify):
//  1. runs are sorted and non-overlapping;
//  2. adjacent runs are merged: run[i+1].start > run[i].end+1 always;
//  3. no empty runs: every run satisfies start <= end (length >= 1);
//  4. no trailing zero-run is ever retained (zero-runs are not stored).
//
// Because of these invariants, any given set has exactly one possible run
// list, and therefore exactly one Bytes() encoding.
package ontology

import (
	"math"
	"slices"
	"sort"
	"sync"
)

// interval is an inclusive run of consecutive set bits [start, end].
type interval struct {
	start, end uint32
}

// Set is a run-length-encoded bitmap set. The zero value is a ready-to-use
// empty set. runs is never mutated in place: mutators install a freshly
// built slice under the write lock, so readers can never observe a
// half-updated (e.g. not-yet-merged) run list. Safe for concurrent use.
type Set struct {
	mu   sync.RWMutex
	runs []interval
}

// New returns an empty set.
func New() *Set { return &Set{} }

// snapshot returns the current run list. The slice is immutable after
// assignment, so sharing it without copying is safe.
func (s *Set) snapshot() []interval {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runs
}

// Set adds bit to the set. Idempotent: setting an already-set bit leaves
// the run list (and hence the encoding) untouched.
func (s *Set) Set(bit uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs = setBit(s.runs, bit)
}

// Clear removes bit from the set. Idempotent: clearing an already-clear
// bit leaves the run list untouched.
func (s *Set) Clear(bit uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs = clearBit(s.runs, bit)
}

// SetRange adds every bit in the inclusive range [lo, hi] in one O(runs)
// pass, merging with any overlapping or adjacent existing runs.
func (s *Set) SetRange(lo, hi uint32) {
	if lo > hi {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs = setRange(s.runs, lo, hi)
}

// Contains reports whether bit is in the set.
func (s *Set) Contains(bit uint32) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return contains(s.runs, bit)
}

// setBit returns a new canonical run list containing bit.
func setBit(runs []interval, bit uint32) []interval {
	i := sort.Search(len(runs), func(k int) bool { return runs[k].end >= bit })
	if i < len(runs) && runs[i].start <= bit {
		return runs // already set
	}
	// left adjacency: runs[i-1].end < bit (else contained above), and
	// end+1 cannot overflow because end == MaxUint32 would contain bit.
	left := i > 0 && runs[i-1].end+1 == bit
	// right adjacency: runs[i].start > bit; bit+1 cannot overflow because
	// bit == MaxUint32 would leave no room for a later run (i == len).
	right := i < len(runs) && bit < math.MaxUint32 && runs[i].start == bit+1
	switch {
	case left && right:
		out := make([]interval, 0, len(runs)-1)
		out = append(out, runs[:i-1]...)
		out = append(out, interval{runs[i-1].start, runs[i].end})
		return append(out, runs[i+1:]...)
	case left:
		out := slices.Clone(runs)
		out[i-1].end = bit
		return out
	case right:
		out := slices.Clone(runs)
		out[i].start = bit
		return out
	default:
		out := make([]interval, 0, len(runs)+1)
		out = append(out, runs[:i]...)
		out = append(out, interval{bit, bit})
		return append(out, runs[i:]...)
	}
}

// clearBit returns a new canonical run list without bit.
func clearBit(runs []interval, bit uint32) []interval {
	i := sort.Search(len(runs), func(k int) bool { return runs[k].end >= bit })
	if i == len(runs) || runs[i].start > bit {
		return runs // already clear
	}
	r := runs[i]
	out := make([]interval, 0, len(runs)+1)
	out = append(out, runs[:i]...)
	if r.start < bit { // bit >= 1 here, so bit-1 cannot underflow
		out = append(out, interval{r.start, bit - 1})
	}
	if bit < r.end { // bit < MaxUint32 here, so bit+1 cannot overflow
		out = append(out, interval{bit + 1, r.end})
	}
	return append(out, runs[i+1:]...)
}

func contains(runs []interval, bit uint32) bool {
	i := sort.Search(len(runs), func(k int) bool { return runs[k].end >= bit })
	return i < len(runs) && runs[i].start <= bit
}

// setRange returns a new canonical run list covering [lo, hi]. All
// comparisons are done in uint64 so that lo == 0 and hi == MaxUint32
// need no special cases.
func setRange(runs []interval, lo, hi uint32) []interval {
	// First run whose end+1 reaches lo (overlap or left-adjacent).
	i := sort.Search(len(runs), func(k int) bool {
		return uint64(runs[k].end)+1 >= uint64(lo)
	})
	// First run at/after i whose start is beyond hi+1 (not right-adjacent).
	j := i
	for j < len(runs) && uint64(runs[j].start) <= uint64(hi)+1 {
		j++
	}
	merged := interval{lo, hi}
	if i < j {
		merged.start = min(merged.start, runs[i].start)
		merged.end = max(merged.end, runs[j-1].end)
	}
	out := make([]interval, 0, len(runs)-(j-i)+1)
	out = append(out, runs[:i]...)
	out = append(out, merged)
	return append(out, runs[j:]...)
}
