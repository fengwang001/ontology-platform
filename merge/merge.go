// Package merge merges overlapping or abutting half-open intervals
// into a sorted, pairwise-disjoint set. It depends only on ontology/iv.
package merge

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"

	"ontology/iv"
)

// ErrInvalidInterval reports an Add of an interval that fails validation.
var ErrInvalidInterval = errors.New("merge: invalid interval")

// Merger maintains a sorted set of pairwise-disjoint intervals.
// The zero value is ready to use and safe for concurrent use.
type Merger struct {
	mu     sync.Mutex
	ranges []iv.Interval
	total  int // historical count of intervals ever added
}

// Add validates v and merges it into the set, coalescing every stored
// interval that overlaps or abuts v (a.End >= b.Start, so [1,2) and
// [2,3) merge into [1,3)).
func (m *Merger) Add(v iv.Interval) error {
	if err := v.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidInterval, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.total++
	// lo: first stored interval starting after v.End; indices >= lo
	// cannot merge. hi: first index in [0, lo) with End >= v.Start;
	// indices < hi end strictly before v. [hi, lo) is the merge window.
	lo := sort.Search(len(m.ranges), func(i int) bool {
		return m.ranges[i].Start > v.End
	})
	hi := sort.Search(lo, func(i int) bool {
		return m.ranges[i].End >= v.Start
	})
	merged := v
	for _, r := range m.ranges[hi:lo] {
		merged.Start = min(merged.Start, r.Start)
		merged.End = max(merged.End, r.End)
	}
	m.ranges = slices.Replace(m.ranges, hi, lo, merged)
	return nil
}

// AddAll adds every interval; the result is independent of order.
func (m *Merger) AddAll(vs []iv.Interval) error {
	for _, v := range vs {
		if err := m.Add(v); err != nil {
			return err
		}
	}
	return nil
}

// Ranges returns a copy of the merged intervals, sorted by Start and
// pairwise disjoint: each End is strictly less than the next Start.
func (m *Merger) Ranges() []iv.Interval {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.ranges)
}

// Total returns the historical count of intervals ever added.
// Merging only shrinks the set, so len(Ranges()) <= Total().
func (m *Merger) Total() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.total
}
