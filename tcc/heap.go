package tcc

// heapEntry 是到期最小堆中的一项，指向 recs 中的 Tried 记录。
type heapEntry struct {
	key      string
	deadline int64
	index    int
}

// expireHeap 按 deadline 维护最小堆。
type expireHeap []*heapEntry

func (h expireHeap) Len() int { return len(h) }

func (h expireHeap) Less(i, j int) bool {
	if h[i].deadline != h[j].deadline {
		return h[i].deadline < h[j].deadline
	}
	return h[i].key < h[j].key
}

func (h expireHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

// Push 实现 heap.Interface，调用方用 container/heap。
func (h *expireHeap) Push(x any) {
	e := x.(*heapEntry)
	e.index = len(*h)
	*h = append(*h, e)
}

// Pop 实现 heap.Interface，调用方用 container/heap。
func (h *expireHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.index = -1
	*h = old[:n-1]
	return e
}
