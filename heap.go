package ontology

// heapItem is one in-flight element of the k-way merge: the current head
// of one input stream.
type heapItem struct {
	value  float64
	stream int
}

// minHeap is a binary min-heap over heapItem ordered by value. It counts
// every comparison between two items so callers can verify the
// O(n log k) bound of the merge.
type minHeap struct {
	items       []heapItem
	comparisons int
}

func (h *minHeap) len() int { return len(h.items) }

// less is the only place items are compared; it does the counting.
func (h *minHeap) less(i, j int) bool {
	h.comparisons++
	return h.items[i].value < h.items[j].value
}

// push inserts it and sifts it up. At most floor(log2(size)) comparisons.
func (h *minHeap) push(it heapItem) {
	h.items = append(h.items, it)
	child := len(h.items) - 1
	for child > 0 {
		parent := (child - 1) / 2
		if !h.less(child, parent) {
			break
		}
		h.items[child], h.items[parent] = h.items[parent], h.items[child]
		child = parent
	}
}

// pop removes and returns the smallest item.
func (h *minHeap) pop() heapItem {
	top := h.items[0]
	last := h.items[len(h.items)-1]
	h.items = h.items[:len(h.items)-1]
	if len(h.items) > 0 {
		h.items[0] = last
		h.siftDown(0)
	}
	return top
}

// siftDown restores the heap property from i downwards. At most
// 2*floor(log2(size)) comparisons (two children per level).
func (h *minHeap) siftDown(i int) {
	for {
		left, right := 2*i+1, 2*i+2
		smallest := i
		if left < len(h.items) && h.less(left, smallest) {
			smallest = left
		}
		if right < len(h.items) && h.less(right, smallest) {
			smallest = right
		}
		if smallest == i {
			return
		}
		h.items[i], h.items[smallest] = h.items[smallest], h.items[i]
		i = smallest
	}
}
