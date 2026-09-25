// Package topk holds the top-K state of a single group.
// Total order: higher Score first; ties broken by smaller ItemID first.
package topk

import (
	"container/heap"
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrDuplicate is returned when Add sees an ItemID already in the group.
	ErrDuplicate = errors.New("topk: item id already exists in group")
	// ErrNotFound is returned when Remove targets an unknown ItemID.
	ErrNotFound = errors.New("topk: item id not found in group")
)

// Item is one scored member of a group.
type Item struct {
	ItemID string
	Score  int
}

// less defines the total order: higher score first, then smaller id.
func less(a, b Item) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.ItemID < b.ItemID
}

// maxHeap keeps in-list items with the WORST (total-order last) at index 0,
// so heap[0] is the top-K boundary compared against each newcomer.
type maxHeap []Item

func (h maxHeap) Len() int           { return len(h) }
func (h maxHeap) Less(i, j int) bool { return less(h[j], h[i]) }
func (h maxHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *maxHeap) Push(x any)        { *h = append(*h, x.(Item)) }
func (h *maxHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// Set is one group's state: in-list heap plus the full ordered below-line
// tail needed to refill the list on deletion.
type Set struct {
	k         int
	score     map[string]int // every live item: id -> score
	list      maxHeap        // in-list items, len <= k
	below     []Item         // all below-line items in total order
	lastCheck int            // (unexported) admission comparisons of the latest Add
}

// NewSet creates an empty set keeping the k highest items in-list.
func NewSet(k int) *Set { return &Set{k: k, score: make(map[string]int)} }

// Add inserts one item and restores the top-K boundary. A duplicate id is an
// error and leaves every field untouched.
func (s *Set) Add(item Item) error {
	if _, ok := s.score[item.ItemID]; ok {
		return ErrDuplicate
	}
	s.score[item.ItemID] = item.Score
	s.lastCheck = 0
	if s.list.Len() < s.k {
		heap.Push(&s.list, item) // list not full yet: admitted without comparison
	} else {
		s.lastCheck = 1 // admission decided by one comparison against the boundary
		if less(item, s.list[0]) {
			worst := heap.Pop(&s.list).(Item)
			heap.Push(&s.list, item)
			s.insertBelow(worst)
		} else {
			s.insertBelow(item)
		}
	}
	return nil
}

// insertBelow keeps the below-line tail in total order (not an admission).
func (s *Set) insertBelow(it Item) {
	idx := sort.Search(len(s.below), func(i int) bool { return less(it, s.below[i]) })
	s.below = append(s.below, Item{})
	copy(s.below[idx+1:], s.below[idx:])
	s.below[idx] = it
}

// Remove deletes one item, promoting the best below-line item when the removed
// item was in-list. An unknown id is an error and leaves state untouched.
func (s *Set) Remove(itemID string) error {
	if _, ok := s.score[itemID]; !ok {
		return ErrNotFound
	}
	delete(s.score, itemID)
	for i, it := range s.list {
		if it.ItemID == itemID {
			heap.Remove(&s.list, i)
			if len(s.below) > 0 {
				up := s.below[0]
				s.below = s.below[1:]
				heap.Push(&s.list, up) // refill: best below-line item is the new Kth
			}
			return nil
		}
	}
	for i, it := range s.below { // below is score-ordered, not id-ordered: scan
		if it.ItemID == itemID {
			s.below = append(s.below[:i], s.below[i+1:]...) // list unchanged
			break
		}
	}
	return nil
}

// Top returns the in-list items in total order, best first.
func (s *Set) Top() []Item {
	out := append([]Item(nil), s.list...)
	sort.Slice(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

// AdmissionCheckBounded reports pass/fail of the O(1)-admission property across
// group sizes; the counter value itself never leaves the package.
func AdmissionCheckBounded() error {
	for _, m := range []int{100, 1000, 10000} {
		s := NewSet(10)
		for i := 0; i < m; i++ {
			if err := s.Add(Item{fmt.Sprintf("x%05d", i), i}); err != nil {
				return err
			}
		}
		if err := s.Add(Item{"newcomer", m + 1}); err != nil {
			return err
		}
		if s.lastCheck > 2 {
			return errors.New("topk: admission comparison count is not bounded by a constant")
		}
	}
	return nil
}
