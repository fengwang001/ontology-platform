package ontology

// minHeap stores elements ordered so that the root is the worst-ranked
// element currently held (most easily evicted).
type minHeap struct {
	dir Direction
	es  []Element
}

func newMinHeap(dir Direction, cap int) *minHeap {
	return &minHeap{dir: dir, es: make([]Element, 0, cap)}
}

func (h *minHeap) len() int { return len(h.es) }

func (h *minHeap) push(e Element) {
	h.es = append(h.es, e)
	h.up(len(h.es) - 1)
}

func (h *minHeap) pop() Element {
	n := len(h.es) - 1
	h.es[0], h.es[n] = h.es[n], h.es[0]
	e := h.es[n]
	h.es = h.es[:n]
	if n > 0 {
		h.down(0)
	}
	return e
}

func (h *minHeap) removeAt(i int) {
	n := len(h.es) - 1
	if i != n {
		h.es[i] = h.es[n]
		h.es = h.es[:n]
		if i < n {
			h.down(i)
			h.up(i)
		}
	} else {
		h.es = h.es[:n]
	}
}

func (h *minHeap) fixAt(i int) {
	if !h.down(i) {
		h.up(i)
	}
}

func (h *minHeap) indexOf(id string) int {
	for i := range h.es {
		if h.es[i].ID == id {
			return i
		}
	}
	return -1
}

func (h *minHeap) at(i int) Element { return h.es[i] }

// less reports whether the element at i is worse-ranked than the one at j,
// i.e. closer to eviction.
func (h *minHeap) less(i, j int) bool {
	return worse(h.dir, h.es[i], h.es[j])
}

func (h *minHeap) swap(i, j int) { h.es[i], h.es[j] = h.es[j], h.es[i] }

func (h *minHeap) up(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !h.less(i, parent) {
			return
		}
		h.swap(i, parent)
		i = parent
	}
}

func (h *minHeap) down(i int) bool {
	n := len(h.es)
	start := i
	for {
		left := 2*i + 1
		if left >= n {
			return i > start
		}
		child := left
		if right := left + 1; right < n && h.less(right, left) {
			child = right
		}
		if !h.less(child, i) {
			return i > start
		}
		h.swap(i, child)
		i = child
	}
}
