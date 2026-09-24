// Package first maintains the FIRST_VALUE (earliest active event) incrementally.
// It depends only on evt.
package first

import (
	"container/heap"

	"ontology/evt"
)

// Set is a multiset of active events with an incremental minimum.
type Set struct {
	h   minHeap
	cnt map[evt.Event]int // occurrences per distinct (Key, TS)
	n   int               // total active occurrences
	// lastCmp counts heap order comparisons made by the most recent
	// Add/Remove while sifting up/down. Unexported: no exported method or
	// function exposes it; only in-package white-box tests may read it.
	lastCmp int
}

// NewSet creates an empty Set.
func NewSet() *Set {
	s := &Set{cnt: map[evt.Event]int{}}
	s.h = minHeap{idx: map[evt.Event]int{}, cmp: &s.lastCmp}
	return s
}

// Add inserts one occurrence. Duplicate (Key, TS) pairs are kept as a
// multiset; only the first occurrence of a distinct event touches the heap.
func (s *Set) Add(e evt.Event) {
	s.lastCmp = 0
	if s.cnt[e] == 0 {
		heap.Push(&s.h, e)
	}
	s.cnt[e]++
	s.n++
}

// Remove withdraws one occurrence. It reports false (and changes nothing)
// when no occurrence of e is active. When the last occurrence is withdrawn
// the entry is extracted from the heap, so the next minimum is promoted.
func (s *Set) Remove(e evt.Event) bool {
	s.lastCmp = 0
	if s.cnt[e] == 0 {
		return false
	}
	s.cnt[e]--
	s.n--
	if s.cnt[e] == 0 {
		heap.Remove(&s.h, s.h.idx[e])
		delete(s.cnt, e)
	}
	return true
}

// First returns the minimum active event by (TS, Key); never cached.
func (s *Set) First() (evt.Event, bool) {
	if s.h.Len() == 0 {
		return evt.Event{}, false
	}
	return s.h.ev[0], true
}

// Count returns the number of active occurrences (with multiplicity).
func (s *Set) Count() int { return s.n }

// LogCostOK reports whether a worst-case sift after m inserts stays within
// c*ceil(log2 m)+k comparisons at several sizes. It exposes only a boolean
// verdict: the comparison count itself remains unexported.
func LogCostOK() bool {
	for _, m := range []int{100, 1000, 10000} {
		s := NewSet()
		for i := 0; i < m; i++ {
			s.Add(evt.Event{Key: logKey(i), TS: int64(i)})
		}
		s.Add(evt.Event{Key: logKey(m), TS: -1})
		bound := 2*(ceilLog2(m+1)) + 2
		if s.lastCmp <= 0 || s.lastCmp > bound {
			return false
		}
	}
	return true
}

func ceilLog2(n int) int {
	h := 0
	for p := 1; p < n; p <<= 1 {
		h++
	}
	return h
}

func logKey(i int) string {
	return string(rune('A'+i%26)) + string(rune('A'+(i/26)%26)) + string(rune('A'+i/676))
}

// minHeap holds one slot per distinct active event and tracks each slot's
// index so withdrawal of any event costs O(log n), not a linear scan.
type minHeap struct {
	ev  []evt.Event
	idx map[evt.Event]int
	cmp *int
}

func (h *minHeap) Len() int { return len(h.ev) }

func (h *minHeap) Less(i, j int) bool {
	*h.cmp++
	return evt.Less(h.ev[i], h.ev[j])
}

func (h *minHeap) Swap(i, j int) {
	h.ev[i], h.ev[j] = h.ev[j], h.ev[i]
	h.idx[h.ev[i]] = i
	h.idx[h.ev[j]] = j
}

func (h *minHeap) Push(x any) {
	e := x.(evt.Event)
	h.idx[e] = len(h.ev)
	h.ev = append(h.ev, e)
}

func (h *minHeap) Pop() any {
	e := h.ev[len(h.ev)-1]
	h.ev = h.ev[:len(h.ev)-1]
	delete(h.idx, e)
	return e
}
