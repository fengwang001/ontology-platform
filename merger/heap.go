package merger

// timerHeap 是带位置索引的二叉最小堆，堆序由 less 定义。
// 通过 pos 索引支持任意元素 O(log n) 删除与键值变化后的修复，
// 取堆顶为 O(1) 且不产生任何键比较。
type timerHeap struct {
	items []*timer
	pos   map[*timer]int
	less  func(a, b *timer) bool
}

func newTimerHeap(less func(a, b *timer) bool) *timerHeap {
	return &timerHeap{pos: make(map[*timer]int), less: less}
}

func (h *timerHeap) len() int { return len(h.items) }

func (h *timerHeap) top() *timer {
	if len(h.items) == 0 {
		return nil
	}
	return h.items[0]
}

func (h *timerHeap) push(t *timer) {
	h.pos[t] = len(h.items)
	h.items = append(h.items, t)
	h.siftUp(len(h.items) - 1)
}

func (h *timerHeap) pop() *timer {
	n := len(h.items) - 1
	top := h.items[0]
	h.swap(0, n)
	h.items = h.items[:n]
	delete(h.pos, top)
	if n > 0 {
		h.siftDown(0)
	}
	return top
}

func (h *timerHeap) remove(t *timer) {
	i, ok := h.pos[t]
	if !ok {
		return
	}
	n := len(h.items) - 1
	h.swap(i, n)
	h.items = h.items[:n]
	delete(h.pos, t)
	if i < n {
		h.siftDown(i)
		h.siftUp(i)
	}
}

// fix 在 t 的键变化后恢复堆序。
func (h *timerHeap) fix(t *timer) {
	i, ok := h.pos[t]
	if !ok {
		return
	}
	h.siftDown(i)
	h.siftUp(i)
}

func (h *timerHeap) swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.pos[h.items[i]] = i
	h.pos[h.items[j]] = j
}

func (h *timerHeap) siftUp(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !h.less(h.items[i], h.items[parent]) {
			break
		}
		h.swap(i, parent)
		i = parent
	}
}

func (h *timerHeap) siftDown(i int) {
	n := len(h.items)
	for {
		left := 2*i + 1
		if left >= n {
			break
		}
		smallest := left
		if right := left + 1; right < n && h.less(h.items[right], h.items[left]) {
			smallest = right
		}
		if !h.less(h.items[smallest], h.items[i]) {
			break
		}
		h.swap(i, smallest)
		i = smallest
	}
}
