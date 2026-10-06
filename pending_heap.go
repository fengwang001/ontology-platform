package waitlist

import "container/heap"

type pendingEntry struct {
	id       int64
	deadline int64
	index    int
}

type pendingHeap []*pendingEntry

func (h pendingHeap) Len() int { return len(h) }

func (h pendingHeap) Less(i, j int) bool {
	if h[i].deadline != h[j].deadline {
		return h[i].deadline < h[j].deadline
	}
	return h[i].id < h[j].id
}

func (h pendingHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *pendingHeap) Push(value any) {
	entry := value.(*pendingEntry)
	entry.index = len(*h)
	*h = append(*h, entry)
}

func (h *pendingHeap) Pop() any {
	old := *h
	entry := old[len(old)-1]
	old[len(old)-1] = nil
	*h = old[:len(old)-1]
	entry.index = -1
	return entry
}

type pendingSet struct {
	items map[int64]*pendingEntry
	heap  pendingHeap
}

func newPendingSet() *pendingSet {
	set := &pendingSet{items: make(map[int64]*pendingEntry)}
	heap.Init(&set.heap)
	return set
}

func (s *pendingSet) push(id int64, deadline int64) {
	entry := &pendingEntry{id: id, deadline: deadline}
	s.items[id] = entry
	heap.Push(&s.heap, entry)
}

func (s *pendingSet) remove(id int64) {
	entry, ok := s.items[id]
	if !ok {
		return
	}
	heap.Remove(&s.heap, entry.index)
	delete(s.items, id)
}

func (s *pendingSet) peek() *pendingEntry {
	if len(s.heap) == 0 {
		return nil
	}
	return s.heap[0]
}

func (s *pendingSet) pop() *pendingEntry {
	entry := heap.Pop(&s.heap).(*pendingEntry)
	delete(s.items, entry.id)
	return entry
}
