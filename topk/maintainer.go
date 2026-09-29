package topk

import (
	"math"
	"slices"
	"sync"
)

// Element is an id/score pair returned to callers.
type Element struct {
	ID    string
	Score float64
}

// Maintainer keeps the highest-scoring k elements with a deterministic
// tie-break: higher score first, then lexicographically smaller id first.
//
// All active elements (including ones outside the top-k view) are kept in a
// single canonical ordering, so the top-k result is always a prefix of that
// ordering and withdrawal of an in-view element lets the next active element
// take its place immediately.
type Maintainer struct {
	mu       sync.RWMutex
	k        int
	capacity int
	scores   map[string]float64
	order    []string
}

// New creates a Maintainer retaining at most k elements from a universe of
// at most capacity distinct active ids.
func New(k, capacity int) (*Maintainer, error) {
	if k <= 0 {
		return nil, ErrNonPositiveK
	}
	if capacity < k {
		return nil, ErrKExceedsCap
	}
	return &Maintainer{
		k:        k,
		capacity: capacity,
		scores:   make(map[string]float64, capacity),
		order:    make([]string, 0, k),
	}, nil
}

// Upsert inserts a new element or overwrites an existing one. It is atomic:
// on error no state changes.
func (m *Maintainer) Upsert(id string, score float64) error {
	if id == "" {
		return ErrEmptyID
	}
	if math.IsNaN(score) || math.IsInf(score, 0) {
		return ErrInvalidScore
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.scores[id]; !exists && len(m.scores) >= m.capacity {
		return ErrCapacityFull
	}

	if idx := slices.Index(m.order, id); idx >= 0 {
		m.order = slices.Delete(m.order, idx, idx+1)
	}
	m.scores[id] = score
	pos := insertionPos(m.scores, m.order, id, score)
	m.order = slices.Insert(m.order, pos, id)
	return nil
}

// Withdraw removes an element; removing an unknown or empty id is an
// idempotent no-op.
func (m *Maintainer) Withdraw(id string) {
	if id == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.scores[id]; !exists {
		return
	}
	if idx := slices.Index(m.order, id); idx >= 0 {
		m.order = slices.Delete(m.order, idx, idx+1)
	}
	delete(m.scores, id)
}

// TopK returns the ordered top n elements. When fewer than n elements are
// active all of them are returned. The result is the prefix of length n of
// the canonical ordering, so top n' for any n' < n equals result[:n'].
func (m *Maintainer) TopK(n int) ([]Element, error) {
	if n <= 0 {
		return nil, ErrNonPositiveK
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	if n > m.k {
		return nil, ErrKExceedsLimit
	}
	if n > len(m.order) {
		n = len(m.order)
	}
	return m.drain(n), nil
}

// Count reports how many active elements are currently retained.
func (m *Maintainer) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.order)
}

// Snapshot returns every active element in canonical order.
func (m *Maintainer) Snapshot() []Element {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.drain(len(m.order))
}

func (m *Maintainer) drain(n int) []Element {
	out := make([]Element, n)
	for i := range n {
		id := m.order[i]
		out[i] = Element{ID: id, Score: m.scores[id]}
	}
	return out
}

// compareElement orders a before b when a has the higher score, breaking
// equal scores by the smaller (lexicographic) id. Returns -1 / 0 / 1.
func compareElement(idA string, scoreA float64, idB string, scoreB float64) int {
	switch {
	case scoreA > scoreB:
		return -1
	case scoreA < scoreB:
		return 1
	case idA < idB:
		return -1
	case idA > idB:
		return 1
	default:
		return 0
	}
}

func insertionPos(scores map[string]float64, order []string, id string, score float64) int {
	pos, _ := slices.BinarySearchFunc(order, id, func(candidate, target string) int {
		return compareElement(candidate, scores[candidate], target, score)
	})
	return pos
}
