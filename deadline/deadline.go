package deadline

type Item struct {
	Req   string
	DueAt int64
}

type Heap struct {
	items []Item
	index map[string]int
}

func New() *Heap {
	return &Heap{index: make(map[string]int)}
}

func (h *Heap) less(i, j int) bool {
	if h.items[i].DueAt != h.items[j].DueAt {
		return h.items[i].DueAt < h.items[j].DueAt
	}
	return h.items[i].Req < h.items[j].Req
}

func (h *Heap) swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.index[h.items[i].Req] = i
	h.index[h.items[j].Req] = j
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

func (h *Heap) down(i int) {
	n := len(h.items)
	for {
		left := 2*i + 1
		if left >= n {
			return
		}
		smallest := left
		if right := left + 1; right < n && h.less(right, left) {
			smallest = right
		}
		if !h.less(smallest, i) {
			return
		}
		h.swap(i, smallest)
		i = smallest
	}
}

// Len reports the number of queued requests.
func (h *Heap) Len() int { return len(h.items) }

// Empty reports whether the heap holds no item.
func (h *Heap) Empty() bool { return len(h.items) == 0 }

// Peek returns the earliest item without removing it.
func (h *Heap) Peek() (Item, bool) {
	if len(h.items) == 0 {
		return Item{}, false
	}
	return h.items[0], true
}

// Push inserts or replaces the item keyed by Req.
func (h *Heap) Push(it Item) {
	if pos, ok := h.index[it.Req]; ok {
		h.items[pos].DueAt = it.DueAt
		h.down(pos)
		h.up(pos)
		return
	}
	h.items = append(h.items, it)
	pos := len(h.items) - 1
	h.index[it.Req] = pos
	h.up(pos)
}

// Pop removes and returns the earliest item.
func (h *Heap) Pop() (Item, bool) {
	if len(h.items) == 0 {
		return Item{}, false
	}
	top := h.items[0]
	n := len(h.items) - 1
	if n > 0 {
		h.items[0] = h.items[n]
		h.index[h.items[0].Req] = 0
	}
	h.items = h.items[:n]
	delete(h.index, top.Req)
	if n > 0 {
		h.down(0)
	}
	return top, true
}

// Delete removes the item keyed by req; it is a no-op if absent.
func (h *Heap) Delete(req string) {
	pos, ok := h.index[req]
	if !ok {
		return
	}
	n := len(h.items) - 1
	if pos != n {
		h.items[pos] = h.items[n]
		h.index[h.items[pos].Req] = pos
	}
	h.items = h.items[:n]
	delete(h.index, req)
	if pos < n {
		h.down(pos)
		h.up(pos)
	}
}

// Has reports whether req is queued.
func (h *Heap) Has(req string) bool {
	_, ok := h.index[req]
	return ok
}
