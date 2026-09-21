package ontology

// item is one heap entry: the current head element of one stream.
// At most one item per stream is ever in the heap, so the heap holds
// at most len(streams) items at any time.
type item struct {
	value  float64
	stream int
}

// streamHeap is a min-heap of items ordered by value. The less
// callback counts element comparisons for Stats.
type streamHeap struct {
	items []item
	less  func(a, b float64) bool
}

func (h *streamHeap) Len() int { return len(h.items) }

func (h *streamHeap) Less(i, j int) bool {
	return h.less(h.items[i].value, h.items[j].value)
}

func (h *streamHeap) Swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
}

func (h *streamHeap) Push(x any) {
	h.items = append(h.items, x.(item))
}

func (h *streamHeap) Pop() any {
	old := h.items
	n := len(old)
	it := old[n-1]
	old[n-1] = item{}
	h.items = old[:n-1]
	return it
}

// peek returns the minimum item without removing it.
func (h *streamHeap) peek() item { return h.items[0] }
