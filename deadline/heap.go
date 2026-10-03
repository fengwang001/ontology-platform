package deadline

// Heap 是按 (At 升序, Kind 升序) 排序的到期最小堆。
// 每个 Kind 至多一项，因此堆规模恒不超过 4。
type Heap struct {
	items []Item
	index [4]int // Kind -> 堆下标 +1；0 表示不存在
}

// NewHeap 创建空堆。
func NewHeap() *Heap {
	h := &Heap{}
	for i := range h.index {
		h.index[i] = 0
	}
	return h
}

// Set 写入或更新一类到期项；at<0 表示移除该项。
func (h *Heap) Set(kind Kind, at int64) {
	if at < 0 {
		h.remove(kind)
		return
	}
	if pos := h.index[kind]; pos > 0 {
		i := pos - 1
		h.items[i].At = at
		h.fix(i)
		return
	}
	h.items = append(h.items, Item{At: at, Kind: kind})
	h.index[kind] = len(h.items)
	h.up(len(h.items) - 1)
}

// Peek 返回堆顶；空堆返回 false。
func (h *Heap) Peek() (Item, bool) {
	if len(h.items) == 0 {
		return Item{}, false
	}
	return h.items[0], true
}

// Pop 移除并返回堆顶；空堆返回 false。
func (h *Heap) Pop() (Item, bool) {
	n := len(h.items)
	if n == 0 {
		return Item{}, false
	}
	top := h.items[0]
	h.index[top.Kind] = 0
	last := h.items[n-1]
	h.items = h.items[:n-1]
	if n > 1 {
		h.items[0] = last
		h.index[last.Kind] = 1
		h.down(0, len(h.items))
	}
	return top, true
}

// Len 返回堆中项数。
func (h *Heap) Len() int { return len(h.items) }

// Clone 返回深拷贝。
func (h *Heap) Clone() *Heap {
	cp := &Heap{items: append([]Item(nil), h.items...), index: h.index}
	return cp
}

func (h *Heap) remove(kind Kind) {
	pos := h.index[kind]
	if pos == 0 {
		return
	}
	i := pos - 1
	n := len(h.items) - 1
	if i != n {
		h.swap(i, n)
	}
	h.items = h.items[:n]
	h.index[kind] = 0
	if i < n {
		h.fix(i)
	}
}

func (h *Heap) less(i, j int) bool {
	a, b := h.items[i], h.items[j]
	return a.At < b.At || (a.At == b.At && a.Kind < b.Kind)
}

func (h *Heap) swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.index[h.items[i].Kind] = i + 1
	h.index[h.items[j].Kind] = j + 1
}

func (h *Heap) fix(i int) {
	if !h.down(i, len(h.items)) {
		h.up(i)
	}
}

func (h *Heap) up(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !h.less(i, parent) {
			return
		}
		h.swap(i, parent)
		i = parent
	}
}

func (h *Heap) down(i, n int) bool {
	moved := false
	for {
		left := 2*i + 1
		if left >= n {
			return moved
		}
		smallest := left
		if right := left + 1; right < n && h.less(right, left) {
			smallest = right
		}
		if !h.less(smallest, i) {
			return moved
		}
		h.swap(i, smallest)
		i = smallest
		moved = true
	}
}
