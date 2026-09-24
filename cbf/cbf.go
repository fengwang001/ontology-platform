// Package cbf implements a deletion-safe counting bloom filter with
// exact per-key insertion counts and overflow rejection.
package cbf

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/hash"
)

// Sentinel errors, mutually distinguishable via errors.Is.
var (
	ErrOverflow    = errors.New("cbf: counter overflow")
	ErrNotInserted = errors.New("cbf: delete of never-inserted key")
)

// Filter is a counting bloom filter. The zero value is not usable; use New.
type Filter struct {
	mu         sync.RWMutex
	m, k       int
	maxCount   uint8
	counters   []uint8
	keys       map[int64]int // exact per-key insertion count
	lastAccess atomic.Int64  // counters touched by the last Insert/Delete/Contains
}

// New builds a filter. Invalid parameters leave no state behind.
func New(m, k int, maxCount uint8) (*Filter, error) {
	if err := hash.Validate(m, k, maxCount); err != nil {
		return nil, err
	}
	return &Filter{
		m: m, k: k, maxCount: maxCount,
		counters: make([]uint8, m),
		keys:     make(map[int64]int),
	}, nil
}

// Insert adds x. If any of its k counters already equals maxCount the whole
// insert is rejected with ErrOverflow and no counter is touched.
func (f *Filter) Insert(x int64) error {
	pos := hash.Positions(x, f.m, f.k)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastAccess.Store(int64(len(pos)))
	for _, p := range pos {
		if f.counters[p] == f.maxCount {
			return ErrOverflow
		}
	}
	for _, p := range pos {
		f.counters[p]++
	}
	f.keys[x]++
	return nil
}

// Delete removes one insertion of x. Deleting a key whose exact insertion
// count is 0 fails with ErrNotInserted and changes nothing.
func (f *Filter) Delete(x int64) error {
	pos := hash.Positions(x, f.m, f.k)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastAccess.Store(int64(len(pos)))
	if f.keys[x] == 0 {
		return ErrNotInserted
	}
	for _, p := range pos {
		f.counters[p]-- // all > 0: x was inserted, so its positions were incremented
	}
	f.keys[x]--
	if f.keys[x] == 0 {
		delete(f.keys, x)
	}
	return nil
}

// Contains reports whether all k counters of x are positive. False positives
// are allowed; false negatives are not. Safe for concurrent use.
func (f *Filter) Contains(x int64) bool {
	pos := hash.Positions(x, f.m, f.k)
	f.mu.RLock()
	defer f.mu.RUnlock()
	f.lastAccess.Store(int64(len(pos)))
	for _, p := range pos {
		if f.counters[p] == 0 {
			return false
		}
	}
	return true
}

// Sum returns the total of all counters; equals k * net insertions.
func (f *Filter) Sum() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	s := 0
	for _, c := range f.counters {
		s += int(c)
	}
	return s
}

// Snapshot returns a copy of the counter array, for verification only.
func (f *Filter) Snapshot() []uint8 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]uint8, len(f.counters))
	copy(out, f.counters)
	return out
}
