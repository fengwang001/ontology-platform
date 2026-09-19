package topk

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
)

// ErrInvalidCapacity is returned by New when K is not positive.
var ErrInvalidCapacity = errors.New("topk: capacity K must be positive")

// Selector is a streaming Top-K selector with fixed capacity. It keeps at
// most K elements at any time, sorted best-first according to the
// composite order defined by its Direction. The zero value is not usable;
// construct one with New. All methods are safe for concurrent use.
type Selector struct {
	mu      sync.Mutex
	k       int
	dir     Direction
	items   []Element // sorted best-first, len(items) <= k at all times
	scores  map[string]float64
	skipped int64
}

// New returns a Selector holding at most k elements. It returns
// ErrInvalidCapacity if k <= 0.
func New(k int, dir Direction) (*Selector, error) {
	if k <= 0 {
		return nil, fmt.Errorf("%w, got %d", ErrInvalidCapacity, k)
	}
	return &Selector{
		k:      k,
		dir:    dir,
		scores: make(map[string]float64),
	}, nil
}

// Push offers one element to the selector. A NaN score is rejected and
// counted by Skipped. Re-pushing an existing ID overwrites its previous
// score and re-ranks it immediately. An element that does not rank within
// the current top K is dropped; whenever the buffer is full the worst
// kept element is evicted to make room for a better newcomer.
func (s *Selector) Push(id string, score float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if math.IsNaN(score) {
		s.skipped++
		return
	}
	if old, ok := s.scores[id]; ok {
		pos := s.search(Element{ID: id, Score: old})
		s.items = append(s.items[:pos], s.items[pos+1:]...)
		delete(s.scores, id)
	}
	e := Element{ID: id, Score: score}
	pos := s.search(e)
	if pos == s.k {
		return // buffer full and e ranks no better than the worst kept
	}
	if len(s.items) == s.k {
		delete(s.scores, s.items[s.k-1].ID)
		s.items = s.items[:s.k-1]
	}
	s.items = append(s.items, Element{})
	copy(s.items[pos+1:], s.items[pos:])
	s.items[pos] = e
	s.scores[id] = score
}

// search returns the index at which e belongs in the sorted items slice:
// the first index whose element does not rank strictly before e.
func (s *Selector) search(e Element) int {
	return sort.Search(len(s.items), func(i int) bool {
		return !less(s.dir, s.items[i], e)
	})
}

// Snapshot returns a copy of the current top elements in ranked order.
// The result is independent of the selector's internal state; mutating it
// affects nothing. It returns all held elements when fewer than K have
// been accepted.
func (s *Selector) Snapshot() []Element {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Element, len(s.items))
	copy(out, s.items)
	return out
}

// Len reports how many elements the selector currently holds. It never
// exceeds the capacity K.
func (s *Selector) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}

// Skipped reports how many pushed elements were rejected because their
// score was NaN.
func (s *Selector) Skipped() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.skipped
}
