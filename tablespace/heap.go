package tablespace

import "container/heap"

// minExtentHeap is a min-heap of extent ids used for lazily locating the
// smallest FREE / non-full FRAG extent. Entries that no longer qualify are
// discarded when observed at the root.
type minExtentHeap []int

func (h minExtentHeap) Len() int           { return len(h) }
func (h minExtentHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h minExtentHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *minExtentHeap) Push(x any) {
	*h = append(*h, x.(int))
}

func (h *minExtentHeap) Pop() any {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}

// PushID pushes an extent id without the heap.Interface wrapper.
func (h *minExtentHeap) PushID(id int) {
	heap.Push(h, id)
}
