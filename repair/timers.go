package repair

type timerEntry struct {
	ticketID int64
	due      int
}

type timerHeap struct {
	entries []timerEntry
}

func (h *timerHeap) push(entry timerEntry) {
	pos := len(h.entries)
	h.entries = append(h.entries, entry)
	for pos > 0 {
		parent := (pos - 1) / 2
		if !h.less(pos, parent) {
			break
		}
		h.swap(pos, parent)
		pos = parent
	}
}

func (h *timerHeap) remove(ticketID int64) {
	for pos, entry := range h.entries {
		if entry.ticketID == ticketID {
			h.removeAt(pos)
			return
		}
	}
}

func (h *timerHeap) peek() (timerEntry, bool) {
	if len(h.entries) == 0 {
		return timerEntry{}, false
	}
	return h.entries[0], true
}

func (h *timerHeap) pop() (timerEntry, bool) {
	if len(h.entries) == 0 {
		return timerEntry{}, false
	}
	entry := h.entries[0]
	h.removeAt(0)
	return entry, true
}

func (h *timerHeap) removeAt(pos int) {
	last := len(h.entries) - 1
	if pos == last {
		h.entries = h.entries[:last]
		return
	}
	h.entries[pos] = h.entries[last]
	h.entries = h.entries[:last]
	h.siftDown(pos)
}

func (h *timerHeap) siftDown(pos int) {
	for {
		left := pos*2 + 1
		if left >= len(h.entries) {
			return
		}
		best := left
		right := left + 1
		if right < len(h.entries) && h.less(right, left) {
			best = right
		}
		if !h.less(best, pos) {
			return
		}
		h.swap(pos, best)
		pos = best
	}
}

func (h *timerHeap) less(a, b int) bool {
	if h.entries[a].due != h.entries[b].due {
		return h.entries[a].due < h.entries[b].due
	}
	return h.entries[a].ticketID < h.entries[b].ticketID
}

func (h *timerHeap) swap(a, b int) {
	h.entries[a], h.entries[b] = h.entries[b], h.entries[a]
}
