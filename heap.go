package ontology

// streamHeap is a min-heap of stream indices ordered by each stream's
// current head value. It holds at most one entry per non-exhausted
// stream, so its length never exceeds the number of streams.
type streamHeap struct {
	idx  []int
	less func(a, b int) bool // compares heads of streams a and b
}

func (h *streamHeap) Len() int { return len(h.idx) }

func (h *streamHeap) Less(i, j int) bool {
	return h.less(h.idx[i], h.idx[j])
}

func (h *streamHeap) Swap(i, j int) {
	h.idx[i], h.idx[j] = h.idx[j], h.idx[i]
}

func (h *streamHeap) Push(x any) {
	h.idx = append(h.idx, x.(int))
}

func (h *streamHeap) Pop() any {
	old := h.idx
	n := len(old)
	v := old[n-1]
	h.idx = old[:n-1]
	return v
}

// top returns the stream index with the smallest head value.
func (h *streamHeap) top() int { return h.idx[0] }
