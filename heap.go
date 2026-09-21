package ontology

// heapItem is one in-flight element of the k-way merge: a value plus the
// index of the stream it was drawn from.
type heapItem struct {
	val    float64
	stream int
}

// minHeap is a hand-rolled binary min-heap so that every comparison can be
// counted. It holds at most one not-yet-emitted element per stream, so its
// size never exceeds the number of streams.
//
// Ordering: by value; ties (including +0.0 vs -0.0, which compare equal)
// are broken by stream index to keep the merge deterministic. NaN never
// reaches the heap because validate rejects it first.
type minHeap struct {
	items []heapItem
	cmp   *int64 // shared comparison counter owned by the Merger
}

func (h *minHeap) len() int { return len(h.items) }

// less reports whether item i orders before item j and counts the
// comparison. It is the only place where heap elements are compared.
func (h *minHeap) less(i, j int) bool {
	*h.cmp++
	a, b := h.items[i], h.items[j]
	if a.val == b.val {
		return a.stream < b.stream
	}
	return a.val < b.val
}

func (h *minHeap) push(it heapItem) {
	h.items = append(h.items, it)
	for child := len(h.items) - 1; child > 0; {
		parent := (child - 1) / 2
		if !h.less(child, parent) {
			break
		}
		h.items[child], h.items[parent] = h.items[parent], h.items[child]
		child = parent
	}
}

func (h *minHeap) pop() heapItem {
	n := len(h.items) - 1
	top := h.items[0]
	h.items[0] = h.items[n]
	h.items = h.items[:n]
	for parent := 0; ; {
		left := 2*parent + 1
		if left >= n {
			break
		}
		smallest := left
		if right := left + 1; right < n && h.less(right, left) {
			smallest = right
		}
		if !h.less(smallest, parent) {
			break
		}
		h.items[smallest], h.items[parent] = h.items[parent], h.items[smallest]
		parent = smallest
	}
	return top
}

// peek returns the minimum element without removing it.
func (h *minHeap) peek() heapItem { return h.items[0] }
