package windowbuf

// heapItem orders buffered entries by (end, key); both the closing set and
// the early-emission candidates are taken from the front of this order.
type heapItem struct {
	end int64
	key string
	ws  int64
}

// emitHeap is a min-heap of heapItem ordered by (end, key) ascending.
type emitHeap []heapItem

func (h emitHeap) Len() int { return len(h) }

func (h emitHeap) Less(i, j int) bool {
	if h[i].end != h[j].end {
		return h[i].end < h[j].end
	}
	return h[i].key < h[j].key
}

func (h emitHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *emitHeap) Push(x any) {
	*h = append(*h, x.(heapItem))
}

func (h *emitHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = heapItem{}
	*h = old[:n-1]
	return item
}
