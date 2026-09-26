// Package cbf holds the m-counter array of a Counting Bloom Filter and
// implements Add / underflow-safe Remove / min-Query. Depends only on ch.
package cbf

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/ch"
)

// The three failure classes are pairwise-distinguishable sentinel errors.
var ErrInvalidParams = errors.New("cbf: invalid parameters (require m > 0 and k > 0)")
var ErrInvalidKey = errors.New("cbf: invalid key (must be non-negative)")
var ErrNotPresent = errors.New("cbf: cannot remove key, a counter is zero")

type Filter struct {
	mu       sync.RWMutex
	hasher   *ch.Hasher
	counters []int64
	// lastQueryTouches: visits by the most recent Query; unexported on
	// purpose, in-package tests read it, callers get only LastQueryTouchedK.
	lastQueryTouches atomic.Int64
}

// New creates a Filter with m counters and k hash functions.
func New(m, k int) (*Filter, error) {
	if m <= 0 || k <= 0 {
		return nil, ErrInvalidParams
	}
	return &Filter{hasher: ch.New(m, k), counters: make([]int64, m)}, nil
}

// Add increments each of the k counters once.
func (f *Filter) Add(key int64) error {
	if key < 0 {
		return ErrInvalidKey
	}
	idx := f.indices(key)
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, i := range idx {
		f.counters[i]++
	}
	return nil
}

// Remove decrements the k counters only when all are >= 1; the precheck
// precedes any decrement, so a rejected call changes nothing.
func (f *Filter) Remove(key int64) error {
	if key < 0 {
		return ErrInvalidKey
	}
	idx := f.indices(key)
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, i := range idx {
		if f.counters[i] == 0 {
			return ErrNotPresent
		}
	}
	for _, i := range idx {
		f.counters[i]--
	}
	return nil
}

// Query returns min over j of c[h_j(key)]; it visits exactly k counters.
func (f *Filter) Query(key int64) (int64, error) {
	if key < 0 {
		return 0, ErrInvalidKey
	}
	idx := f.indices(key)
	f.mu.RLock()
	defer f.mu.RUnlock()
	minimum, n := f.counters[idx[0]], int64(1)
	for _, i := range idx[1:] {
		n++
		if v := f.counters[i]; v < minimum {
			minimum = v
		}
	}
	f.lastQueryTouches.Store(n)
	return minimum, nil
}

// Snapshot returns a copy of the counter array.
func (f *Filter) Snapshot() []int64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]int64, len(f.counters))
	copy(out, f.counters)
	return out
}

// LastQueryTouchedK reports whether the last Query visited exactly k
// counters, without exposing the count itself.
func (f *Filter) LastQueryTouchedK() bool {
	return f.lastQueryTouches.Load() == int64(f.hasher.K())
}

func (f *Filter) indices(key int64) []int {
	idx := make([]int, f.hasher.K())
	f.hasher.Indices(key, idx)
	return idx
}

// SelfCheck replays Add(3) Add(5) Add(7) Remove(5) on a fresh 8/3 filter
// against a naive model and verifies the four invariants. Local state only.
func (f *Filter) SelfCheck() bool {
	g, _ := New(8, 3)
	model := make([]int64, 8)
	step := func(key int64, d int64) bool {
		var err error
		if d == 1 {
			err = g.Add(key)
		} else {
			err = g.Remove(key)
		}
		if err != nil {
			return false
		}
		for _, i := range g.indices(key) {
			model[i] += d
		} // invariant 3: naive replay
		return slices.Equal(g.Snapshot(), model)
	}
	if !step(3, 1) || !step(5, 1) || !step(7, 1) || !step(5, -1) {
		return false
	}
	q3, _ := g.Query(3) // invariant 1: no false negative
	q5, _ := g.Query(5)
	q7, _ := g.Query(7)
	if q3 < 1 || q5 != 0 || q7 < 1 {
		return false
	}
	before := g.Snapshot() // invariant 4: rejected op leaves no trace
	if err := g.Remove(5); err != ErrNotPresent || !slices.Equal(g.Snapshot(), before) {
		return false
	}
	one, _ := New(8, 3) // invariant 2: a lone Add then Remove restores all zero
	zero := make([]int64, 8)
	return one.Add(4) == nil && one.Remove(4) == nil && slices.Equal(one.Snapshot(), zero)
}
