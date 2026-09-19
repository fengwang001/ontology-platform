package topk

import (
	"math"
	"sort"
)

// Push ingests one element.
//
// A NaN score is rejected: the element does not participate in ranking and
// the skipped counter is incremented. Re-pushing an existing ID overwrites
// its previous score, taking effect immediately (the element may drop out
// of the top K). When the selector is full, a new element is admitted only
// if it ranks strictly before the current worst element.
func (s *Selector) Push(id string, score float64) {
	if math.IsNaN(score) {
		s.mu.Lock()
		s.skipped++
		s.mu.Unlock()
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	e := Element{ID: id, Score: score}
	if _, ok := s.index[id]; ok {
		s.removeLocked(id)
	} else if len(s.items) == s.k && !s.less(e, s.items[len(s.items)-1]) {
		return
	}
	s.insertLocked(e)
	if len(s.items) > s.k {
		s.removeLocked(s.items[len(s.items)-1].ID)
	}
}

// insertLocked places e into its sorted position. Caller holds the lock.
func (s *Selector) insertLocked(e Element) {
	i := sort.Search(len(s.items), func(i int) bool {
		return !s.less(s.items[i], e)
	})
	s.items = append(s.items, Element{})
	copy(s.items[i+1:], s.items[i:])
	s.items[i] = e
	s.index[e.ID] = i
	for j := i + 1; j < len(s.items); j++ {
		s.index[s.items[j].ID] = j
	}
}

// removeLocked deletes the element with the given ID. Caller holds the lock.
func (s *Selector) removeLocked(id string) {
	i, ok := s.index[id]
	if !ok {
		return
	}
	copy(s.items[i:], s.items[i+1:])
	s.items = s.items[:len(s.items)-1]
	delete(s.index, id)
	for j := i; j < len(s.items); j++ {
		s.index[s.items[j].ID] = j
	}
}
