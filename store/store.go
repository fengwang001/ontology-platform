// Package store maintains the ordered, contiguous partition list and
// places keys into partitions. It depends only on package part.
package store

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/part"
)

// Sentinel errors; each rejected operation maps to exactly one of these.
var (
	ErrBadParam      = errors.New("store: invalid parameters")
	ErrKeyOutOfRange = errors.New("store: key out of range")
	ErrKeyNotFound   = errors.New("store: key not found")
)

// Range is an immutable snapshot of one partition.
type Range struct {
	Lo, Hi int64
	Load   int
	Keys   []int64 // sorted
}

// Store keeps partitions sorted by Lo, contiguous and non-overlapping,
// exactly covering [low, high).
type Store struct {
	mu             sync.RWMutex
	low, high      int64
	splitThreshold int
	mergeThreshold int
	parts          []*part.Part
	lastCmp        atomic.Int64 // partitions compared by the latest Locate
}

// New validates parameters before touching any state.
func New(low, high int64, splitThreshold, mergeThreshold int) (*Store, error) {
	if low < 0 || low >= high || splitThreshold < 2 || mergeThreshold < 1 || splitThreshold <= mergeThreshold {
		return nil, ErrBadParam
	}
	return &Store{low: low, high: high, splitThreshold: splitThreshold,
		mergeThreshold: mergeThreshold, parts: []*part.Part{part.New(low, high)}}, nil
}

// find binary-searches the partition holding key; caller holds the lock.
func (s *Store) find(key int64) int {
	lo, hi := 0, len(s.parts)-1
	var n int64
	for lo <= hi {
		mid := (lo + hi) / 2
		n++
		p := s.parts[mid]
		switch {
		case key < p.Lo:
			hi = mid - 1
		case key >= p.Hi:
			lo = mid + 1
		default:
			s.lastCmp.Store(n)
			return mid
		}
	}
	s.lastCmp.Store(n)
	return -1 // unreachable while the range invariant holds
}

// Insert adds key; afterwards it splits the holding partition once if
// its load reached the threshold. Validation precedes any mutation.
func (s *Store) Insert(key int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key < s.low || key >= s.high {
		return ErrKeyOutOfRange
	}
	i := s.find(key)
	p := s.parts[i]
	p.Keys[key] = struct{}{}
	if p.Load() >= s.splitThreshold {
		l, r := p.Split()
		tail := append([]*part.Part{l, r}, s.parts[i+1:]...)
		s.parts = append(s.parts[:i], tail...)
	}
	return nil
}

// Delete removes key. A key not held by any partition (including one
// outside [low, high)) yields ErrKeyNotFound; state is untouched.
func (s *Store) Delete(key int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key < s.low || key >= s.high {
		return ErrKeyNotFound
	}
	i := s.find(key)
	p := s.parts[i]
	if _, ok := p.Keys[key]; !ok {
		return ErrKeyNotFound
	}
	delete(p.Keys, key)
	return nil
}

// Locate returns the snapshot of the partition holding key.
func (s *Store) Locate(key int64) (Range, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if key < s.low || key >= s.high {
		return Range{}, ErrKeyOutOfRange
	}
	return snapshot(s.parts[s.find(key)]), nil
}

// Compact greedily merges adjacent pairs whose combined load is within
// the merge threshold, left to right, in a single pass.
func (s *Store) Compact() {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*part.Part, 0, len(s.parts))
	for _, p := range s.parts {
		if n := len(out); n > 0 && part.CanMerge(out[n-1], p, s.mergeThreshold) {
			out[n-1] = part.Merge(out[n-1], p)
		} else {
			out = append(out, p)
		}
	}
	s.parts = out
}

// Ranges returns snapshots of all partitions in order.
func (s *Store) Ranges() []Range {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Range, len(s.parts))
	for i, p := range s.parts {
		out[i] = snapshot(p)
	}
	return out
}

func snapshot(p *part.Part) Range {
	keys := make([]int64, 0, len(p.Keys))
	for k := range p.Keys {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return Range{Lo: p.Lo, Hi: p.Hi, Load: len(keys), Keys: keys}
}
