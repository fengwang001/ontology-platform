package policy

// endHeap 是 (effectiveDay, seq) 的最小堆，index 记录堆下标。
// 堆中只保存未生效批改（预约或待补缴），已生效即移除，
// 因此“找出下一条应生效批改”为 O(log n)，
// 不随历史已生效批改数与缴费记录数增长。
type endHeap struct {
	items []*endorsement
	index map[string]int
}

func newEndHeap() endHeap {
	return endHeap{index: map[string]int{}}
}

func (h *endHeap) len() int { return len(h.items) }

func (h *endHeap) less(i, j int) bool {
	a, b := h.items[i], h.items[j]
	if a.effectiveDay != b.effectiveDay {
		return a.effectiveDay < b.effectiveDay
	}
	return a.seq < b.seq
}

func (h *endHeap) swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.index[h.items[i].endID] = i
	h.index[h.items[j].endID] = j
}

func (h *endHeap) up(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !h.less(i, parent) {
			return
		}
		h.swap(i, parent)
		i = parent
	}
}

func (h *endHeap) down(i, n int) {
	for {
		left := 2*i + 1
		if left >= n {
			return
		}
		small := left
		if right := left + 1; right < n && h.less(right, left) {
			small = right
		}
		if !h.less(small, i) {
			return
		}
		h.swap(i, small)
		i = small
	}
}

// push 加入一条未生效批改。
func (h *endHeap) push(en *endorsement) {
	if _, ok := h.index[en.endID]; ok {
		return
	}
	h.index[en.endID] = len(h.items)
	h.items = append(h.items, en)
	h.up(len(h.items) - 1)
}

// remove 按批改号移除并返回该批改；不存在返回 nil。
func (h *endHeap) remove(endID string) *endorsement {
	idx, ok := h.index[endID]
	if !ok {
		return nil
	}
	return h.removeAt(idx)
}

func (h *endHeap) removeAt(idx int) *endorsement {
	n := len(h.items) - 1
	en := h.items[idx]
	h.swap(idx, n)
	h.items[n] = nil
	h.items = h.items[:n]
	delete(h.index, en.endID)
	if idx < n {
		h.down(idx, n)
		h.up(idx)
	}
	return en
}

// pop 移除并返回生效次序最靠前的批改。
func (h *endHeap) pop() *endorsement {
	if len(h.items) == 0 {
		return nil
	}
	return h.removeAt(0)
}

// peek 返回生效次序最靠前的批改但不移除。
func (h *endHeap) peek() *endorsement {
	if len(h.items) == 0 {
		return nil
	}
	return h.items[0]
}
