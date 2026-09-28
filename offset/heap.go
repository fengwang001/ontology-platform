package offset

// offsetHeap 是最小堆，存放某分区内"已确认但尚未被提交位点覆盖"的位点。
// push/pop 均为 O(log n)；推进提交位点时每次只弹出堆顶，
// 弹出总数不超过确认总数，与在途规模无关。
type offsetHeap struct {
	items []uint64
}

func (h *offsetHeap) len() int { return len(h.items) }

func (h *offsetHeap) push(v uint64) {
	h.items = append(h.items, v)
	for i := len(h.items) - 1; i > 0; {
		parent := (i - 1) / 2
		if h.items[parent] <= h.items[i] {
			break
		}
		h.items[parent], h.items[i] = h.items[i], h.items[parent]
		i = parent
	}
}

func (h *offsetHeap) peek() (uint64, bool) {
	if len(h.items) == 0 {
		return 0, false
	}
	return h.items[0], true
}

func (h *offsetHeap) pop() uint64 {
	top := h.items[0]
	last := len(h.items) - 1
	h.items[0] = h.items[last]
	h.items = h.items[:last]
	for i := 0; ; {
		l, r := 2*i+1, 2*i+2
		smallest := i
		if l < len(h.items) && h.items[l] < h.items[smallest] {
			smallest = l
		}
		if r < len(h.items) && h.items[r] < h.items[smallest] {
			smallest = r
		}
		if smallest == i {
			break
		}
		h.items[i], h.items[smallest] = h.items[smallest], h.items[i]
		i = smallest
	}
	return top
}
