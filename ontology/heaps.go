package ontology

type indexedHeap struct {
	items       []*timer
	less        func(left, right *timer) bool
	index       func(t *timer) int
	setIndex    func(t *timer, index int)
	examined    uint64
	comparisons uint64
}

func (h *indexedHeap) resetCounters() {
	h.examined = 0
	h.comparisons = 0
}

func newDeadlineHeap() *indexedHeap {
	return &indexedHeap{
		less: func(left, right *timer) bool {
			if left.deadline != right.deadline {
				return left.deadline < right.deadline
			}
			return left.id < right.id
		},
		index:    func(t *timer) int { return t.deadlineIndex },
		setIndex: func(t *timer, index int) { t.deadlineIndex = index },
	}
}

func newNextHeap() *indexedHeap {
	return &indexedHeap{
		less: func(left, right *timer) bool {
			if left.next != right.next {
				return left.next < right.next
			}
			return left.id < right.id
		},
		index:    func(t *timer) int { return t.nextIndex },
		setIndex: func(t *timer, index int) { t.nextIndex = index },
	}
}

func newReadyHeap() *indexedHeap {
	return &indexedHeap{
		less: func(left, right *timer) bool {
			if left.deadline != right.deadline {
				return left.deadline < right.deadline
			}
			return left.id < right.id
		},
		index:    func(t *timer) int { return t.readyIndex },
		setIndex: func(t *timer, index int) { t.readyIndex = index },
	}
}

func (h *indexedHeap) len() int {
	return len(h.items)
}

func (h *indexedHeap) peek() *timer {
	if len(h.items) == 0 {
		return nil
	}
	return h.items[0]
}

func (h *indexedHeap) push(t *timer) {
	h.setIndex(t, len(h.items))
	h.items = append(h.items, t)
	h.up(len(h.items) - 1)
}

func (h *indexedHeap) pop() *timer {
	h.examined++
	return h.remove(0)
}

func (h *indexedHeap) popNextIfAtOrBefore(at uint64) (*timer, bool) {
	if len(h.items) == 0 {
		return nil, false
	}
	h.examined++
	if h.items[0].next > at {
		return nil, false
	}
	return h.remove(0), true
}

func (h *indexedHeap) remove(index int) *timer {
	if index < 0 || index >= len(h.items) {
		return nil
	}
	last := len(h.items) - 1
	h.swap(index, last)
	t := h.items[last]
	h.items = h.items[:last]
	h.setIndex(t, -1)
	if index < last {
		h.down(index)
		h.up(index)
	}
	return t
}

func (h *indexedHeap) up(index int) {
	for index > 0 {
		parent := (index - 1) / 2
		h.comparisons++
		if !h.less(h.items[index], h.items[parent]) {
			return
		}
		h.swap(index, parent)
		index = parent
	}
}

func (h *indexedHeap) down(index int) {
	for {
		smallest := index
		left := index*2 + 1
		right := left + 1
		if left < len(h.items) {
			h.comparisons++
		}
		if left < len(h.items) && h.less(h.items[left], h.items[smallest]) {
			smallest = left
		}
		if right < len(h.items) {
			h.comparisons++
		}
		if right < len(h.items) && h.less(h.items[right], h.items[smallest]) {
			smallest = right
		}
		if smallest == index {
			return
		}
		h.swap(index, smallest)
		index = smallest
	}
}

func (h *indexedHeap) swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.setIndex(h.items[i], i)
	h.setIndex(h.items[j], j)
}
