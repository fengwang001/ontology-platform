package ontology

// entry is one weighted element kept in the reservoir. key is the
// A-Res priority key computed as u^(1/w) with u ~ Uniform(0,1).
type entry struct {
	value  string
	weight int
	key    float64
}

// minHeap is a binary min-heap of entries ordered by key. The root is
// the entry with the smallest key, i.e. the first candidate to be
// evicted when a better element arrives.
type minHeap []entry

func (h minHeap) Len() int { return len(h) }

func (h minHeap) less(i, j int) bool { return h[i].key < h[j].key }

func (h minHeap) swap(i, j int) { h[i], h[j] = h[j], h[i] }

// push inserts e and restores the heap invariant. O(log n).
func (h *minHeap) push(e entry) {
	*h = append(*h, e)
	h.up(len(*h) - 1)
}

// pop removes and returns the minimum entry. O(log n).
func (h *minHeap) pop() entry {
	old := *h
	n := len(old)
	top := old[0]
	old[0] = old[n-1]
	*h = old[:n-1]
	if n-1 > 0 {
		h.down(0)
	}
	return top
}

// peek returns the minimum entry without removing it.
func (h minHeap) peek() entry { return h[0] }

// replaceTop swaps the root with e and restores the heap invariant.
func (h *minHeap) replaceTop(e entry) {
	(*h)[0] = e
	h.down(0)
}

func (h minHeap) up(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !h.less(i, parent) {
			break
		}
		h.swap(i, parent)
		i = parent
	}
}

func (h minHeap) down(i int) {
	n := len(h)
	for {
		left := 2*i + 1
		if left >= n {
			break
		}
		smallest := left
		if right := left + 1; right < n && h.less(right, left) {
			smallest = right
		}
		if !h.less(smallest, i) {
			break
		}
		h.swap(i, smallest)
		i = smallest
	}
}
