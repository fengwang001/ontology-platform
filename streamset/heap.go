package streamset

// heapItem is one in-flight element: the current head of one stream.
type heapItem struct {
	value  float64
	stream int
}

// minHeap is a binary min-heap over stream heads, ordered by value with
// the stream index as a deterministic tie-breaker. It counts every
// ordering comparison in comparisons.
//
// Cost bound: with k streams the heap holds at most k items. Each push
// costs at most floor(log2(k)) comparisons (sift up), each pop at most
// 2*floor(log2(k)) comparisons (two child comparisons per sift-down
// level). Every input element is pushed and popped exactly once, so for
// n total elements and k >= 2:
//
//	comparisons <= 3 * n * ceil(log2(k))
//
// and for k <= 1 no comparisons happen at all. This is linear in n.
type minHeap struct {
	items       []heapItem
	comparisons int64
}

func (h *minHeap) len() int { return len(h.items) }

// less orders items and counts the comparison. NaN never reaches the
// heap (validated away), and +0.0 == -0.0, so this is a total order.
func (h *minHeap) less(i, j int) bool {
	h.comparisons++
	if h.items[i].value != h.items[j].value {
		return h.items[i].value < h.items[j].value
	}
	return h.items[i].stream < h.items[j].stream
}

func (h *minHeap) push(it heapItem) {
	h.items = append(h.items, it)
	i := len(h.items) - 1
	for i > 0 {
		parent := (i - 1) / 2
		if !h.less(i, parent) {
			break
		}
		h.items[i], h.items[parent] = h.items[parent], h.items[i]
		i = parent
	}
}

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
