package check

import (
	"sort"
	"sync"

	"ontology/heap"
)

type BruteHeap struct {
	mu      sync.Mutex
	entries []heap.Entry[int]
	gone    map[int]bool
	nextID  int
}

func NewBruteHeap() *BruteHeap { return &BruteHeap{gone: map[int]bool{}} }

func (h *BruteHeap) Push(val int) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.nextID
	h.nextID++
	h.entries = append(h.entries, heap.Entry[int]{ID: id, Val: val})
	h.sort()
	return id
}

func (h *BruteHeap) Peek() (heap.Entry[int], bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.entries) == 0 {
		return heap.Entry[int]{}, false
	}
	return h.entries[0], true
}

func (h *BruteHeap) Pop() (heap.Entry[int], bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.entries) == 0 {
		return heap.Entry[int]{}, false
	}
	e := h.entries[0]
	h.entries, h.gone[e.ID] = h.entries[1:], true
	return e, true
}

func (h *BruteHeap) DecreaseKey(id, val int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.entries {
		if h.entries[i].ID != id {
			continue
		}
		if val >= h.entries[i].Val {
			return heap.ErrNotSmaller
		}
		h.entries[i].Val = val
		h.sort()
		return nil
	}
	if h.gone[id] {
		return heap.ErrGone
	}
	return heap.ErrUnknown
}

func (h *BruteHeap) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.entries)
}

func (h *BruteHeap) Drain() []heap.Entry[int] {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := append([]heap.Entry[int](nil), h.entries...)
	return out
}

func (h *BruteHeap) sort() {
	sort.Slice(h.entries, func(i, j int) bool { return h.entries[i].Val < h.entries[j].Val })
}
