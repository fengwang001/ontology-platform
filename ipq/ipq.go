// Package ipq implements an indexed binary min-heap keyed by
// (priority, registration sequence) with O(log n) update and delete.
package ipq

// Item is one heap element. Seq is the global registration order assigned
// by the caller; it never changes after Push.
type Item struct {
	ID  string
	Pri int
	Seq int64
}

// Heap is an indexed min-heap. checked counts heap nodes inspected or
// swapped during the most recent Update/Delete; it is unexported and
// never exposed through any exported API.
type Heap struct {
	items   []Item
	pos     map[string]int
	checked int
}

// New returns an empty heap.
func New() *Heap { return &Heap{pos: make(map[string]int)} }

// Len reports the number of elements.
func (h *Heap) Len() int { return len(h.items) }

func (h *Heap) Contains(id string) bool { _, ok := h.pos[id]; return ok }

// Peek returns the minimum element without removing it.
func (h *Heap) Peek() (Item, bool) {
	if len(h.items) == 0 {
		return Item{}, false
	}
	return h.items[0], true
}

// Push inserts it; the caller must guarantee it.ID is not present.
func (h *Heap) Push(it Item) {
	h.pos[it.ID] = len(h.items)
	h.items = append(h.items, it)
	h.siftUp(len(h.items) - 1)
}

// Pop removes and returns the minimum element.
func (h *Heap) Pop() (Item, bool) {
	if len(h.items) == 0 {
		return Item{}, false
	}
	top := h.items[0]
	h.removeAt(0)
	return top, true
}

// Update changes the priority of id: smaller sifts up (decrease-key),
// larger sifts down (increase-key), equal is a no-op.
func (h *Heap) Update(id string, pri int) bool {
	i, ok := h.pos[id]
	if !ok {
		return false
	}
	h.checked = 0
	old := h.items[i].Pri
	h.items[i].Pri = pri
	switch {
	case pri < old:
		h.siftUp(i)
	case pri > old:
		h.siftDown(i)
	}
	return true
}

// Delete removes id entirely and clears its index mapping.
func (h *Heap) Delete(id string) bool {
	i, ok := h.pos[id]
	if !ok {
		return false
	}
	h.checked = 0
	h.removeAt(i)
	return true
}

// removeAt deletes items[i] and restores the heap by sifting the
// swapped-in last element whichever direction is needed.
func (h *Heap) removeAt(i int) {
	n := len(h.items) - 1
	removed := h.items[i].ID
	if i != n {
		h.swap(i, n)
	}
	delete(h.pos, removed)
	h.items = h.items[:n]
	if i < n {
		if i > 0 && h.less(i, (i-1)/2) {
			h.siftUp(i)
		} else {
			h.siftDown(i)
		}
	}
}

// less orders by (priority asc, seq asc).
func (h *Heap) less(i, j int) bool {
	a, b := h.items[i], h.items[j]
	return a.Pri < b.Pri || (a.Pri == b.Pri && a.Seq < b.Seq)
}

func (h *Heap) swap(i, j int) {
	h.checked++
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.pos[h.items[i].ID] = i
	h.pos[h.items[j].ID] = j
}

func (h *Heap) siftUp(i int) {
	for i > 0 {
		p := (i - 1) / 2
		h.checked++
		if !h.less(i, p) {
			return
		}
		h.swap(i, p)
		i = p
	}
}

func (h *Heap) siftDown(i int) {
	for {
		l, r, m := 2*i+1, 2*i+2, i
		if l < len(h.items) {
			h.checked++
			if h.less(l, m) {
				m = l
			}
		}
		if r < len(h.items) {
			h.checked++
			if h.less(r, m) {
				m = r
			}
		}
		if m == i {
			return
		}
		h.swap(i, m)
		i = m
	}
}
