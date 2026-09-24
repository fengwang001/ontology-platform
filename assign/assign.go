// Package assign owns partition-to-member ownership of one consumer group.
package assign

import (
	"cmp"
	"container/heap"
	"maps"
	"slices"
	"strconv"
)

// State is the in-memory ownership table; create one with New.
type State struct {
	n       int
	held    map[string]map[int]struct{}
	orphan  map[int]struct{} // partitions currently without an owner
	checked int              // unexported: ownership touches in the latest Rebalance
}

// New creates a table for n partitions with no members: all are orphaned.
func New(n int) *State {
	s := &State{n: n, held: map[string]map[int]struct{}{}, orphan: map[int]struct{}{}}
	for p := range n {
		s.orphan[p] = struct{}{}
	}
	return s
}

// Rebalance performs exactly one redistribution for the full post-change
// member set and returns the migration count: partitions owned by one
// member before the batch and by a different member afterwards.
func (s *State) Rebalance(members []string) (migrations int) {
	s.checked = 0
	next := make(map[string]struct{}, len(members))
	for _, id := range members {
		next[id] = struct{}{}
		if _, ok := s.held[id]; !ok {
			s.held[id] = map[int]struct{}{} // joiner starts empty
		}
	}
	prev := map[int]string{} // previous owner of partitions freed in this batch
	for id, set := range s.held {
		if _, ok := next[id]; ok {
			continue
		}
		for p := range set { // leaver: every held partition becomes an orphan
			prev[p] = id
			s.checked++
			s.orphan[p] = struct{}{}
		}
		delete(s.held, id)
	}
	if len(next) == 0 {
		return 0
	}
	ids := slices.Sorted(maps.Keys(next))
	// Quotas: pre-batch held count descending, ties broken by ID ascending.
	slices.SortFunc(ids, func(a, b string) int {
		return cmp.Or(cmp.Compare(len(s.held[b]), len(s.held[a])), cmp.Compare(a, b))
	})
	base, extra := s.n/len(ids), s.n%len(ids)
	quota := make(map[string]int, len(ids))
	for i, id := range ids {
		quota[id] = base
		if i < extra {
			quota[id]++
		}
		if len(s.held[id]) <= quota[id] {
			continue
		}
		ps := slices.Sorted(maps.Keys(s.held[id])) // release largest numbers first
		for len(s.held[id]) > quota[id] {
			p := ps[len(ps)-1]
			ps = ps[:len(ps)-1]
			delete(s.held[id], p)
			prev[p] = id
			s.checked++
			s.orphan[p] = struct{}{}
		}
	}
	h := &memberHeap{ids: ids, quota: quota, held: s.held}
	heap.Init(h)
	for _, p := range slices.Sorted(maps.Keys(s.orphan)) { // orphans out ascending
		m := heap.Pop(h).(string)
		if old := prev[p]; old != "" && old != m {
			migrations++
		}
		s.held[m][p] = struct{}{}
		s.checked++
		delete(s.orphan, p)
		heap.Push(h, m)
	}
	return migrations
}

// Held returns a sorted copy of the partition set held by each member.
func (s *State) Held() map[string][]int {
	out := make(map[string][]int, len(s.held))
	for id, set := range s.held {
		out[id] = slices.Sorted(maps.Keys(set))
	}
	return out
}

// BoundedCheck reports whether a leave-one rebalance touches only a
// constant number of partitions, independent of the member count m.
// The counter value itself never leaves the package.
func BoundedCheck() bool {
	for _, m := range []int{100, 1000, 10000} {
		s := New(m)
		ids := make([]string, m)
		for i := range ids {
			ids[i] = strconv.Itoa(i)
		}
		s.Rebalance(ids)
		if mig := s.Rebalance(ids[:m-1]); s.checked > mig+5 {
			return false
		}
	}
	return true
}

// memberHeap orders members by (quota - held) descending, ID ascending.
type memberHeap struct {
	ids   []string
	quota map[string]int
	held  map[string]map[int]struct{}
}

func (h *memberHeap) Len() int { return len(h.ids) }
func (h *memberHeap) Less(i, j int) bool {
	a, b := h.ids[i], h.ids[j]
	da := h.quota[a] - len(h.held[a])
	db := h.quota[b] - len(h.held[b])
	return da > db || (da == db && a < b)
}
func (h *memberHeap) Swap(i, j int) { h.ids[i], h.ids[j] = h.ids[j], h.ids[i] }
func (h *memberHeap) Push(x any)    { h.ids = append(h.ids, x.(string)) }
func (h *memberHeap) Pop() any {
	x := h.ids[len(h.ids)-1]
	h.ids = h.ids[:len(h.ids)-1]
	return x
}
