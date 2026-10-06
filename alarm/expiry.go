package alarm

import "container/heap"

type expiryItem struct {
	pointID string
	until   int64
	version int64
}

type expiryHeap []expiryItem

func (h expiryHeap) Len() int { return len(h) }

func (h expiryHeap) Less(left, right int) bool {
	if h[left].until != h[right].until {
		return h[left].until < h[right].until
	}
	return h[left].pointID < h[right].pointID
}

func (h expiryHeap) Swap(left, right int) { h[left], h[right] = h[right], h[left] }

func (h *expiryHeap) Push(value any) {
	*h = append(*h, value.(expiryItem))
}

func (h *expiryHeap) Pop() any {
	old := *h
	item := old[len(old)-1]
	*h = old[:len(old)-1]
	return item
}

var _ heap.Interface = (*expiryHeap)(nil)
