package hashdir

// uint32Heap is a min-heap of minor numbers (container/heap interface).
type uint32Heap []uint32

func (h uint32Heap) Len() int           { return len(h) }
func (h uint32Heap) Less(i, j int) bool { return h[i] < h[j] }
func (h uint32Heap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *uint32Heap) Push(x any)        { *h = append(*h, x.(uint32)) }
func (h *uint32Heap) Pop() any {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}
