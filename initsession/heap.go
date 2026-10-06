package initsession

// unitHeap is a min-heap of unit indices ordered by index, which equals
// source order. Ties cannot happen (every unit occurs at most once).
type unitHeap []int

func (h unitHeap) Len() int           { return len(h) }
func (h unitHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h unitHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *unitHeap) Push(x any) { *h = append(*h, x.(int)) }

func (h *unitHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
