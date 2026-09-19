package topk

// idHeap is a binary heap whose root is the worst-ranked held ID.
// "Less" therefore means "ranks further behind", so Pop removes the
// element that must be evicted at the K/K+1 boundary. Scores live in
// the owning Selector's map; the heap stores IDs only.
type idHeap struct {
	ids   []string
	score map[string]float64
	dir   Direction
}

func (h *idHeap) Len() int { return len(h.ids) }

func (h *idHeap) less(i, j int) bool {
	a, b := h.ids[i], h.ids[j]
	return worse(h.dir, h.score[a], a, h.score[b], b)
}

func (h *idHeap) swap(i, j int) {
	h.ids[i], h.ids[j] = h.ids[j], h.ids[i]
}

func (h *idHeap) up(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !h.less(i, parent) {
			return
		}
		h.swap(i, parent)
		i = parent
	}
}

func (h *idHeap) down(i, n int) {
	for {
		left := 2*i + 1
		if left >= n {
			return
		}
		worst := left
		if right := left + 1; right < n && h.less(right, left) {
			worst = right
		}
		if !h.less(worst, i) {
			return
		}
		h.swap(i, worst)
		i = worst
	}
}

func (h *idHeap) Push(id string) {
	h.ids = append(h.ids, id)
	h.up(len(h.ids) - 1)
}

func (h *idHeap) Pop() string {
	return h.RemoveAt(0)
}

func (h *idHeap) Peek() string {
	if len(h.ids) == 0 {
		return ""
	}
	return h.ids[0]
}

func (h *idHeap) RemoveAt(i int) string {
	n := len(h.ids) - 1
	id := h.ids[i]
	if i != n {
		h.swap(i, n)
		h.ids = h.ids[:n]
		if i < n {
			h.down(i, n)
			h.up(i)
		}
	} else {
		h.ids = h.ids[:n]
	}
	return id
}

func (h *idHeap) FixAt(i int) {
	h.down(i, len(h.ids))
	h.up(i)
}

func (h *idHeap) IndexOf(id string) int {
	for i, held := range h.ids {
		if held == id {
			return i
		}
	}
	return -1
}
