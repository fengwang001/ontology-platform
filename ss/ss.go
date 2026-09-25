// Package ss implements the SpaceSaving update rules on top of heap.
package ss

import (
	"sort"

	"ontology/heap"
)

// Entry is one counter: (Key, Count, Err).
type Entry = heap.Counter

// Summary holds at most k counters.
type Summary struct {
	h *heap.Heap
	k int
}

// New returns an empty summary with capacity k (k must be >= 1).
func New(k int) *Summary { return &Summary{h: heap.New(), k: k} }

// Add processes one event:
//   - monitored key: increment its counter;
//   - room left: add counter (key, 1, 0);
//   - full: replace the min counter (ties: smallest key) with
//     (key, min+1, min).
func (s *Summary) Add(key int) {
	if _, ok := s.h.Get(key); ok {
		s.h.Inc(key)
		return
	}
	if s.h.Len() < s.k {
		s.h.Add(Entry{Key: key, Count: 1})
		return
	}
	m := s.h.Min()
	s.h.ReplaceMin(Entry{Key: key, Count: m.Count + 1, Err: m.Count})
}

// Query returns the overestimate for key (0 if unmonitored).
func (s *Summary) Query(key int) int {
	if c, ok := s.h.Get(key); ok {
		return c.Count
	}
	return 0
}

// TopK returns all counters sorted by Count desc, then Key asc.
func (s *Summary) TopK() []Entry {
	items := s.h.Items()
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		return items[i].Key < items[j].Key
	})
	return items
}
