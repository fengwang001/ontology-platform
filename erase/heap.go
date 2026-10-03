package erase

type deadlineKey struct {
	id       int
	deadline int
}

type deadlineHeap struct {
	items    []*deadlineKey
	index    map[int]int
	activeAt map[int]bool
	examined int
}

func newDeadlineHeap() *deadlineHeap {
	return &deadlineHeap{index: make(map[int]int), activeAt: make(map[int]bool)}
}

func (h *deadlineHeap) len() int {
	return len(h.activeAt)
}

func (h *deadlineHeap) push(id, deadline int) {
	if h.activeAt[id] {
		return
	}
	h.activeAt[id] = true
	if _, ok := h.index[id]; ok {
		h.items[h.index[id]].deadline = deadline
		position := h.index[id]
		h.down(position)
		h.up(position)
		return
	}
	key := &deadlineKey{id: id, deadline: deadline}
	h.items = append(h.items, key)
	position := len(h.items) - 1
	h.index[id] = position
	h.up(position)
}

func (h *deadlineHeap) peek() *deadlineKey {
	h.compact()
	if len(h.items) == 0 {
		return nil
	}
	h.examined++
	return h.items[0]
}

func (h *deadlineHeap) compact() {
	for len(h.items) > 0 && !h.activeAt[h.items[0].id] {
		last := len(h.items) - 1
		h.swap(0, last)
		delete(h.index, h.items[last].id)
		h.items = h.items[:last]
		if len(h.items) > 0 {
			h.down(0)
		}
	}
}

func (h *deadlineHeap) pop() *deadlineKey {
	h.compact()
	if len(h.items) == 0 {
		return nil
	}
	key := h.items[0]
	last := len(h.items) - 1
	if last != 0 {
		h.swap(0, last)
	}
	h.items = h.items[:last]
	delete(h.index, key.id)
	delete(h.activeAt, key.id)
	if len(h.items) > 0 {
		h.down(0)
	}
	return key
}

func (h *deadlineHeap) remove(id int) {
	if !h.activeAt[id] {
		return
	}
	delete(h.activeAt, id)
}

func (h *deadlineHeap) examinedCount() int {
	return h.examined
}

func (h *deadlineHeap) resetExamined() {
	h.examined = 0
}

func (h *deadlineHeap) less(a, b *deadlineKey) bool {
	if a.deadline != b.deadline {
		return a.deadline < b.deadline
	}
	return a.id < b.id
}

func (h *deadlineHeap) up(position int) {
	for position > 0 {
		parent := (position - 1) / 2
		if !h.less(h.items[position], h.items[parent]) {
			return
		}
		h.swap(position, parent)
		position = parent
	}
}

func (h *deadlineHeap) down(position int) {
	for position < len(h.items) {
		smallest := position
		left := position*2 + 1
		right := left + 1
		if left < len(h.items) && h.less(h.items[left], h.items[smallest]) {
			smallest = left
		}
		if right < len(h.items) && h.less(h.items[right], h.items[smallest]) {
			smallest = right
		}
		if smallest == position {
			return
		}
		h.swap(position, smallest)
		position = smallest
	}
}

func (h *deadlineHeap) swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.index[h.items[i].id] = i
	h.index[h.items[j].id] = j
}
