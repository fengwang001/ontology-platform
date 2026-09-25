package ontology

// heap 是固定在 TopK 上的最小堆：heap[0] 是当前保留集合中
// 按 heapLess 定义的"最差"元素，即新元素到来时最先被挑战的位置。
// 堆中每个 ID 至多出现一次，由 TopK 层配合 map 保证。
type heap struct {
	dir   Direction
	items []Element
}

func (h *heap) len() int { return len(h.items) }

func (h *heap) less(i, j int) bool {
	return heapLess(h.dir, h.items[i], h.items[j])
}

func (h *heap) swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
}

func (h *heap) push(e Element) {
	h.items = append(h.items, e)
	h.up(len(h.items) - 1)
}

// fix 把位置 i 的元素按当前值向上或向下调整。
func (h *heap) fix(i int) {
	if !h.down(i, len(h.items)) {
		h.up(i)
	}
}

// remove 删除位置 i 的元素并保持堆性质。
func (h *heap) remove(i int) Element {
	n := len(h.items) - 1
	if n != i {
		h.swap(i, n)
		h.down(i, n)
	}
	old := h.items[n]
	h.items = h.items[:n]
	return old
}

func (h *heap) up(j int) {
	for {
		i := (j - 1) / 2
		if i == j || !h.less(j, i) {
			return
		}
		h.swap(i, j)
		j = i
	}
}

func (h *heap) down(i0, n int) bool {
	i := i0
	for {
		smallest := i
		if l := 2*i + 1; l < n && h.less(l, smallest) {
			smallest = l
		}
		if r := 2*i + 2; r < n && h.less(r, smallest) {
			smallest = r
		}
		if smallest == i {
			return i > i0
		}
		h.swap(i, smallest)
		i = smallest
	}
}
