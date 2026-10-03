package deadline

// less 定义堆序：先按到期时刻；同刻按 SC、S2C、HB、S2S、Wake 取前。
func less(a, b At) bool {
	if a.Time != b.Time {
		return a.Time < b.Time
	}
	return a.Kind < b.Kind
}

// Heap 是到期项最小堆；同刻按 SC、S2C、HB、S2S 次序弹出。
type Heap struct {
	items []At
}

// NewHeap 以给定项构造堆（复制入参切片）。
func NewHeap(items ...At) *Heap {
	h := &Heap{items: append([]At(nil), items...)}
	n := len(h.items)
	for i := n/2 - 1; i >= 0; i-- {
		h.down(i, n)
	}
	return h
}

func (h *Heap) Len() int { return len(h.items) }

// Items 返回堆内全部到期项的副本（顺序即堆的内部顺序）。
func (h *Heap) Items() []At { return append([]At(nil), h.items...) }

func (h *Heap) Push(a At) {
	h.items = append(h.items, a)
	h.up(len(h.items) - 1)
}

func (h *Heap) Peek() (At, bool) {
	if len(h.items) == 0 {
		return At{}, false
	}
	return h.items[0], true
}

func (h *Heap) Pop() (At, bool) {
	n := len(h.items)
	if n == 0 {
		return At{}, false
	}
	top := h.items[0]
	last := n - 1
	h.items[0] = h.items[last]
	h.items = h.items[:last]
	if last > 0 {
		h.down(0, last)
	}
	return top, true
}

func (h *Heap) up(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !less(h.items[i], h.items[parent]) {
			return
		}
		h.items[i], h.items[parent] = h.items[parent], h.items[i]
		i = parent
	}
}

func (h *Heap) down(i, n int) {
	for {
		left := 2*i + 1
		if left >= n {
			return
		}
		smallest := left
		if right := left + 1; right < n && less(h.items[right], h.items[left]) {
			smallest = right
		}
		if !less(h.items[smallest], h.items[i]) {
			return
		}
		h.items[i], h.items[smallest] = h.items[smallest], h.items[i]
		i = smallest
	}
}
