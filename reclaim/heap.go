package reclaim

// minHeap is a min-heap of free slot indexes, so recycling always hands out
// the lowest currently available index first.
type minHeap []uint32

// PushIndex inserts an index into the heap.
func (h *minHeap) PushIndex(v uint32) {
	*h = append(*h, v)
	h.up(len(*h) - 1)
}

// PopIndex removes and returns the smallest index.
func (h *minHeap) PopIndex() uint32 {
	heap := *h
	n := len(heap)
	root := heap[0]
	last := heap[n-1]
	*h = heap[:n-1]
	if n > 1 {
		(*h)[0] = last
		h.down(0)
	}
	return root
}

func (h minHeap) up(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if h[parent] <= h[i] {
			return
		}
		h[parent], h[i] = h[i], h[parent]
		i = parent
	}
}

func (h minHeap) down(i int) {
	n := len(h)
	for {
		left := 2*i + 1
		if left >= n {
			return
		}
		smallest := left
		if right := left + 1; right < n && h[right] < h[left] {
			smallest = right
		}
		if h[i] <= h[smallest] {
			return
		}
		h[i], h[smallest] = h[smallest], h[i]
		i = smallest
	}
}
