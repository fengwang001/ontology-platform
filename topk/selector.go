package topk

import (
	"errors"
	"math"
	"sort"
	"sync"
)

// ErrNonPositiveCapacity is returned by New when K <= 0.
var ErrNonPositiveCapacity = errors.New("topk: capacity K must be positive")

// Selector is a fixed-capacity streaming Top-K selector.
// It never holds more than K items internally, regardless of how
// many items are pushed. All methods are safe for concurrent use.
type Selector struct {
	mu      sync.Mutex
	k       int
	h       *itemHeap
	skipped uint64
}

// New returns a Selector keeping the best K items under dir.
// It returns ErrNonPositiveCapacity if k <= 0.
func New(k int, dir Direction) (*Selector, error) {
	if k <= 0 {
		return nil, ErrNonPositiveCapacity
	}
	return &Selector{k: k, h: newItemHeap(dir)}, nil
}

// Push streams one element. A NaN score is rejected and counted in
// Skipped. Re-pushing an existing ID overwrites its score and
// re-ranks it immediately, even if it falls out of the Top-K.
func (s *Selector) Push(id string, score float64) {
	if math.IsNaN(score) {
		s.mu.Lock()
		s.skipped++
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	it := Item{ID: id, Score: score}
	if idx, ok := s.h.pos[id]; ok {
		s.h.update(idx, it)
		return
	}
	if s.h.Len() < s.k {
		s.h.add(it)
		return
	}
	if better(it, s.h.root(), s.h.dir) {
		s.h.evictRoot()
		s.h.add(it)
	}
}

// Snapshot returns the current Top-K items in strict rank order.
// The result is a copy and shares no state with the selector.
func (s *Selector) Snapshot() []Item {
	s.mu.Lock()
	out := make([]Item, len(s.h.items))
	copy(out, s.h.items)
	dir := s.h.dir
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return better(out[i], out[j], dir) })
	return out
}

// Len returns the number of items currently held, never exceeding K.
func (s *Selector) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.h.Len()
}

// Skipped returns how many pushes were rejected for NaN scores.
func (s *Selector) Skipped() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.skipped
}
