package ontology

import (
	"container/heap"
	"errors"
	"math"
	"sort"
	"sync"
)

// ErrInvalidCapacity is returned when a selector is created with K <= 0.
var ErrInvalidCapacity = errors.New("selector capacity must be greater than zero")

// Selector maintains at most K elements in process memory.
type Selector struct {
	mu        sync.Mutex
	direction Direction
	capacity  int
	ranked    *elementHeap
	skipped   int
}

// NewSelector creates a fixed-capacity top-K selector.
func NewSelector(k int, direction Direction) (*Selector, error) {
	if k <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &Selector{
		direction: direction,
		capacity:  k,
		ranked:    newElementHeap(direction),
	}, nil
}

// Push adds an element, updates a duplicate ID, or skips a NaN score.
func (s *Selector) Push(id string, score float64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if math.IsNaN(score) {
		s.skipped++
		return false
	}

	entry := Element{ID: id, Score: score}
	if index, exists := s.ranked.position[id]; exists {
		if !betterInRank(entry, s.ranked.peek(), s.direction) {
			heap.Remove(s.ranked, index)
			return true
		}
		heap.Remove(s.ranked, index)
		heap.Push(s.ranked, entry)
		return true
	}

	if s.ranked.Len() == s.capacity && !betterInRank(entry, s.ranked.peek(), s.direction) {
		return false
	}
	if s.ranked.Len() == s.capacity {
		heap.Pop(s.ranked)
	}
	heap.Push(s.ranked, entry)

	return true
}

// Snapshot returns the current ranked elements in descending rank order.
func (s *Selector) Snapshot() []Element {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := append([]Element(nil), s.ranked.elements...)
	sort.Slice(result, func(i, j int) bool {
		return betterInRank(result[i], result[j], s.direction)
	})
	return result
}

// Len returns the number of elements currently retained.
func (s *Selector) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ranked.Len()
}

// Skipped returns the number of rejected NaN pushes.
func (s *Selector) Skipped() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.skipped
}
