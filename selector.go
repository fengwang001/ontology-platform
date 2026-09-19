package ontology

import "sync"

// Element is a ranked (ID, Score) pair.
type Element struct {
	ID    string
	Score float64
}

// Selector is a fixed-capacity, concurrency-safe streaming top-K selector.
// The zero value is not usable; construct it with New.
type Selector struct {
	mu      sync.Mutex
	k       int
	dir     Direction
	h       *minHeap
	skipped int64
}

// New constructs a Selector holding at most k elements.
func New(k int, dir Direction) (*Selector, error) {
	if k <= 0 {
		return nil, ErrInvalidCapacity
	}
	if !dir.valid() {
		return nil, ErrInvalidDirection
	}
	return &Selector{
		k:   k,
		dir: dir,
		h:   newMinHeap(dir, k),
	}, nil
}

// Push inserts or overwrites an element. NaN scores are rejected.
func (s *Selector) Push(id string, score float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if isNaN(score) {
		s.skipped++
		return
	}

	e := Element{ID: id, Score: score}
	if idx := s.h.indexOf(id); idx >= 0 {
		s.applyUpdate(idx, e)
		return
	}

	if s.h.len() < s.k {
		s.h.push(e)
		return
	}

	// Full: keep the new element only if it beats the current worst.
	if better(s.dir, e, s.h.at(0)) {
		_ = s.h.pop()
		s.h.push(e)
	}
}

// Snapshot returns the current top K elements in rank order.
func (s *Selector) Snapshot() []Element {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := s.h.len()
	out := make([]Element, n)
	copy(out, s.h.es)
	rankedSort(s.dir, out)
	return out
}

// Len returns the number of elements currently held internally.
func (s *Selector) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.h.len()
}

// Skipped returns the number of rejected (NaN) pushes.
func (s *Selector) Skipped() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.skipped
}

// applyUpdate changes an existing ID's score in place, evicting it when the
// new score no longer belongs among the held elements.
func (s *Selector) applyUpdate(idx int, e Element) {
	old := s.h.at(idx)
	if sameScore(old.Score, e.Score) {
		return
	}
	if s.h.len() < s.k {
		s.h.es[idx] = e
		s.h.fixAt(idx)
		return
	}
	// At capacity the element must re-earn its slot: compare the candidate
	// against every other held element and drop it if it is not in the top K.
	s.h.removeAt(idx)
	if s.h.len() == 0 {
		s.h.push(e)
		return
	}
	worst := s.h.at(0)
	if better(s.dir, e, worst) {
		s.h.push(e)
	}
}
