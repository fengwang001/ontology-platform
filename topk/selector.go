package topk

import "math"

// New creates a Selector that keeps at most k elements. Desc keeps the
// k largest scores; Asc keeps the k smallest. A non-positive k is a
// programmer error and returns ErrInvalidK instead of panicking.
func New(k int, dir Direction) (*Selector, error) {
	if k <= 0 {
		return nil, ErrInvalidK
	}
	s := &Selector{
		k:     k,
		dir:   dir,
		score: make(map[string]float64, k),
	}
	s.held = idHeap{ids: make([]string, 0, k), score: s.score, dir: dir}
	return s, nil
}

// Push inserts an element or replaces the score of an existing ID.
// A NaN score is rejected (the prior score, if any, is left intact)
// and counted in Skipped. A replacement is treated as a removal of
// the old entry followed by a fresh arrival: when the selector is
// full, the updated entry competes with the other k-1 held elements
// and may evict itself, so an ID whose score worsens past the K
// boundary drops out immediately.
func (s *Selector) Push(id string, score float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if math.IsNaN(score) {
		s.skipped++
		return
	}

	if idx := s.held.IndexOf(id); idx >= 0 {
		s.score[id] = score
		s.held.FixAt(idx)
		if s.held.Len() == s.k && s.held.Peek() == id {
			s.held.Pop()
			delete(s.score, id)
		}
		return
	}

	if s.held.Len() < s.k {
		s.score[id] = score
		s.held.Push(id)
		return
	}

	worst := s.held.Peek()
	if better(s.dir, score, id, s.score[worst], worst) {
		s.held.Pop()
		delete(s.score, worst)
		s.score[id] = score
		s.held.Push(id)
	}
}

// Snapshot returns the currently held elements in strict rank order.
// The returned slice is a detached copy and never shares state with
// the selector.
func (s *Selector) Snapshot() []Element {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Element, 0, s.held.Len())
	for _, id := range s.held.ids {
		out = append(out, Element{ID: id, Score: s.score[id]})
	}
	bestFirst(s.dir, out)
	return out
}

// Len reports how many elements the selector currently holds; it
// never exceeds k.
func (s *Selector) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.held.Len()
}

// Skipped reports how many Push calls were rejected for NaN scores.
func (s *Selector) Skipped() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.skipped
}
