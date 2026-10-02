package ontology

type timerHeap struct {
	entries []*idleEntry
}

func (h *timerHeap) Len() int {
	return len(h.entries)
}

func (h *timerHeap) PushTimer(entry *idleEntry) {
	entry.index = len(h.entries)
	h.entries = append(h.entries, entry)
	h.up(entry.index)
}

func (h *timerHeap) PeekTimer() *idleEntry {
	if len(h.entries) == 0 {
		return nil
	}
	return h.entries[0]
}

func (h *timerHeap) PopTimer() *idleEntry {
	if len(h.entries) == 0 {
		return nil
	}
	root := h.entries[0]
	last := len(h.entries) - 1
	h.swap(0, last)
	h.entries[last] = nil
	h.entries = h.entries[:last]
	if len(h.entries) > 0 {
		h.down(0)
	}
	root.index = -1
	return root
}

func (h *timerHeap) RemoveAt(index int) {
	if index < 0 || index >= len(h.entries) {
		return
	}
	last := len(h.entries) - 1
	removed := h.entries[index]
	if index != last {
		h.swap(index, last)
	}
	h.entries[last] = nil
	h.entries = h.entries[:last]
	if index < len(h.entries) {
		h.down(index)
		h.up(index)
	}
	removed.index = -1
}

func (h *timerHeap) ChangedAt(index int) {
	if index < 0 || index >= len(h.entries) {
		return
	}
	h.down(index)
	h.up(index)
}

func (h *timerHeap) DueCount(deadline int64) int {
	return h.dueCountAt(0, deadline)
}

func (h *timerHeap) dueCountAt(index int, deadline int64) int {
	if index >= len(h.entries) || h.entries[index].timer > deadline {
		return 0
	}
	return 1 + h.dueCountAt(index*2+1, deadline) + h.dueCountAt(index*2+2, deadline)
}

func (h *timerHeap) less(i, j int) bool {
	left := h.entries[i]
	right := h.entries[j]
	if left.timer != right.timer {
		return left.timer < right.timer
	}
	return left.key < right.key
}

func (h *timerHeap) swap(i, j int) {
	h.entries[i], h.entries[j] = h.entries[j], h.entries[i]
	h.entries[i].index = i
	h.entries[j].index = j
}

func (h *timerHeap) up(index int) {
	for index > 0 {
		parent := (index - 1) / 2
		if !h.less(index, parent) {
			return
		}
		h.swap(index, parent)
		index = parent
	}
}

func (h *timerHeap) down(index int) {
	for {
		smallest := index
		left := index*2 + 1
		right := left + 1
		if left < len(h.entries) && h.less(left, smallest) {
			smallest = left
		}
		if right < len(h.entries) && h.less(right, smallest) {
			smallest = right
		}
		if smallest == index {
			return
		}
		h.swap(index, smallest)
		index = smallest
	}
}
