package heap

import (
	"cmp"
	"errors"
	"sync"
)

var ErrGone = errors.New("heap item has been removed")
var ErrUnknown = errors.New("unknown heap item id")
var ErrNotSmaller = errors.New("new key is not smaller than current key")

type Heap[T cmp.Ordered] struct {
	mu    sync.Mutex
	order []int
	vals  []T
	pos   []int
	swaps int
}

func New[T cmp.Ordered]() *Heap[T] { return &Heap[T]{} }

func (h *Heap[T]) Len() int { h.mu.Lock(); n := len(h.order); h.mu.Unlock(); return n }

func (h *Heap[T]) Push(val T) int {
	h.mu.Lock()
	id := len(h.pos)
	h.pos = append(h.pos, len(h.order))
	h.vals = append(h.vals, val)
	h.order = append(h.order, id)
	h.swaps = h.up(len(h.order) - 1)
	h.mu.Unlock()
	return id
}

func (h *Heap[T]) Peek() (T, bool) {
	h.mu.RLock()
	val, ok := h.peek()
	h.mu.RUnlock()
	return val, ok
}

func (h *Heap[T]) Pop() (T, bool) {
	h.mu.Lock()
	val, ok := h.peek()
	if !ok {
		h.mu.Unlock()
		return val, false
	}
	root := h.order[0]
	last := len(h.order) - 1
	h.swap(0, last)
	h.order = h.order[:last]
	h.pos[root] = -1
	h.swaps = 0
	if len(h.order) > 0 {
		h.swaps = h.down(0)
	}
	h.mu.Unlock()
	return val, true
}

func (h *Heap[T]) DecreaseKey(id int, val T) (err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case id < 0 || id >= len(h.pos):
		err = ErrUnknown
	case h.pos[id] < 0:
		err = ErrGone
	case val >= h.vals[id]:
		err = ErrNotSmaller
	default:
		index := h.pos[id]
		h.vals[id] = val
		h.swaps = h.up(index)
	}
	return err
}

func (h *Heap[T]) LastAdjustmentSwaps() int { h.mu.Lock(); n := h.swaps; h.mu.Unlock(); return n }

func (h *Heap[T]) up(index int) int {
	n := 0
	for index > 0 {
		parent := (index - 1) / 2
		if h.vals[h.order[index]] >= h.vals[h.order[parent]] {
			break
		}
		h.swap(index, parent)
		index, n = parent, n+1
	}
	return n
}

func (h *Heap[T]) down(index int) int {
	n := 0
	for left := index*2 + 1; left < len(h.order); left = index*2 + 1 {
		child := left
		if right := left + 1; right < len(h.order) && h.vals[h.order[right]] < h.vals[h.order[left]] {
			child = right
		}
		if h.vals[h.order[index]] <= h.vals[h.order[child]] {
			break
		}
		h.swap(index, child)
		index, n = child, n+1
	}
	return n
}

func (h *Heap[T]) swap(i, j int) {
	h.order[i], h.order[j] = h.order[j], h.order[i]
	h.pos[h.order[i]], h.pos[h.order[j]] = i, j
}

func (h *Heap[T]) peek() (T, bool) {
	if len(h.order) == 0 {
		var zero T
		return zero, false
	}
	return h.vals[h.order[0]], true
}
