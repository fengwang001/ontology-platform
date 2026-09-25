// Package csync maintains a set of clocks: sorted interval endpoints,
// binary-search CountAt, and the consensus interval. Depends on marz.
package csync

import (
	"errors"
	"sort"
	"sync"

	"ontology/marz"
)

var (
	// ErrNegativeError: err < 0 would invert the interval (lo > hi).
	ErrNegativeError = errors.New("csync: negative error inverts interval")
	// ErrDuplicateID: a clock with this ID already exists.
	ErrDuplicateID = errors.New("csync: duplicate clock id")
	// ErrNoConsensus: no point is covered by the required number of clocks.
	ErrNoConsensus = errors.New("csync: no consensus interval")
)

// Clock is one clock's offset and error bound.
type Clock struct {
	Offset int64
	Err    int64
}

// Set is a concurrency-safe collection of clocks. Interval endpoints are
// kept as two sorted slices: lo (lower edges) and hi (upper edges).
type Set struct {
	mu     sync.RWMutex
	clocks map[string]Clock
	lo     []int64 // sorted lower edges (L endpoints)
	hi     []int64 // sorted upper edges (R endpoints)

	cmu     sync.Mutex
	checked int // endpoints probed by the most recent CountAt
}

// NewSet returns an empty clock set.
func NewSet() *Set {
	return &Set{clocks: map[string]Clock{}}
}

// Len returns the number of clocks.
func (s *Set) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.clocks)
}

// Add validates first, then inserts the clock and its two endpoints.
// A rejected Add changes no state.
func (s *Set) Add(id string, offset, err int64) error {
	if err < 0 {
		return ErrNegativeError
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.clocks[id]; dup {
		return ErrDuplicateID
	}
	l, r := marz.Endpoints(offset, err)
	s.clocks[id] = Clock{Offset: offset, Err: err}
	s.lo = insertSorted(s.lo, l.Pos)
	s.hi = insertSorted(s.hi, r.Pos)
	return nil
}

func insertSorted(xs []int64, v int64) []int64 {
	i := sort.Search(len(xs), func(i int) bool { return xs[i] >= v })
	return append(xs[:i], append([]int64{v}, xs[i:]...)...)
}

// CountAt returns how many clocks' closed error intervals contain t:
// (lower edges <= t) minus (upper edges < t), each by binary search.
func (s *Set) CountAt(t int64) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	a := upper(s.lo, t, &n) // count of lo[i] <= t
	b := lower(s.hi, t, &n) // count of hi[i] < t
	s.cmu.Lock()
	s.checked = n
	s.cmu.Unlock()
	return a - b
}

// upper returns the count of xs[i] <= t (first index with xs[i] > t).
func upper(xs []int64, t int64, probed *int) int {
	lo, hi := 0, len(xs)
	for lo < hi {
		*probed++
		if mid := (lo + hi) / 2; xs[mid] <= t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// lower returns the count of xs[i] < t (first index with xs[i] >= t).
func lower(xs []int64, t int64, probed *int) int {
	lo, hi := 0, len(xs)
	for lo < hi {
		*probed++
		if mid := (lo + hi) / 2; xs[mid] < t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// Consensus merges the sorted edges (L before R at equal positions) and
// sweeps for the shortest interval covered by at least need clocks.
func (s *Set) Consensus(need int) (lo, hi int64, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	eps := make([]marz.Endpoint, 0, len(s.lo)+len(s.hi))
	i, j := 0, 0
	for i < len(s.lo) || j < len(s.hi) {
		if j >= len(s.hi) || (i < len(s.lo) && s.lo[i] <= s.hi[j]) {
			eps = append(eps, marz.Endpoint{Pos: s.lo[i], Delta: 1})
			i++
		} else {
			eps = append(eps, marz.Endpoint{Pos: s.hi[j], Delta: -1})
			j++
		}
	}
	lo, hi, ok := marz.Sweep(eps, need)
	if !ok {
		return 0, 0, ErrNoConsensus
	}
	return lo, hi, nil
}
