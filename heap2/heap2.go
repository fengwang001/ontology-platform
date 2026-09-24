// Package heap2 provides min and max heaps for int64 values.
package heap2

import "errors"

// ErrEmpty is returned when an empty heap is popped or peeked.
var ErrEmpty = errors.New("heap2: heap is empty")

// Heap is an int64 binary heap ordered by less.
type Heap struct {
	data []int64
	less func(a, b int64) bool
}

// NewMin returns a min-heap.
func NewMin() *Heap { return &Heap{less: func(a, b int64) bool { return a < b }} }

// NewMax returns a max-heap.
func NewMax() *Heap { return &Heap{less: func(a, b int64) bool { return a > b }} }

// Len returns the number of values.
func (h *Heap) Len() int { return len(h.data) }

// Peek returns the root without removing it.
func (h *Heap) Peek() (int64, error) {
	if len(h.data) == 0 {
		return 0, ErrEmpty
	}
	return h.data[0], nil
}

// Push adds v.
func (h *Heap) Push(v int64) {
	h.data = append(h.data, v)
	h.up(len(h.data) - 1)
}

// Pop removes and returns the root.
func (h *Heap) Pop() (int64, error) {
	if len(h.data) == 0 {
		return 0, ErrEmpty
	}
	root := h.data[0]
	last := len(h.data) - 1
	h.data[0] = h.data[last]
	h.data = h.data[:last]
	if last > 0 {
		h.down(0)
	}
	return root, nil
}

func (h *Heap) up(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !h.less(h.data[i], h.data[parent]) {
			return
		}
		h.data[i], h.data[parent] = h.data[parent], h.data[i]
		i = parent
	}
}

func (h *Heap) down(i int) {
	for {
		left, right, best := 2*i+1, 2*i+2, i
		if left < len(h.data) && h.less(h.data[left], h.data[best]) {
			best = left
		}
		if right < len(h.data) && h.less(h.data[right], h.data[best]) {
			best = right
		}
		if best == i {
			return
		}
		h.data[i], h.data[best] = h.data[best], h.data[i]
		i = best
	}
}
