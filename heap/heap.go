package heap

import (
	"errors"
	"sync"
)

var (
	ErrGone       = errors.New("heap: element already popped")
	ErrUnknown    = errors.New("heap: unknown element id")
	ErrNotSmaller = errors.New("heap: new value is not smaller than current key")
)

type Heap[T any] struct {
	mu          sync.RWMutex
	less        func(a, b T) bool
	ids         []int
	vals        []T
	pos         []int
	state       []uint8
	next, swaps int
}

func New[T any](less func(a, b T) bool) *Heap[T] {
	return &Heap[T]{less: less, ids: []int{-1}, vals: []T{*new(T)}}
}
func (h *Heap[T]) Push(v T) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.next
	h.next++
	h.pos, h.state = append(h.pos, len(h.ids)), append(h.state, 1)
	h.ids, h.vals = append(h.ids, id), append(h.vals, v)
	h.up(len(h.ids) - 1)
	return id
}
func (h *Heap[T]) Peek() (v T, ok bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if len(h.ids) > 1 {
		return h.vals[1], true
	}
	return v, false
}
func (h *Heap[T]) Pop() (id int, v T, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.ids) == 1 {
		return 0, v, false
	}
	var last int
	id, v, last = h.ids[1], h.vals[1], len(h.ids)-1
	if last > 1 {
		h.ids[1], h.vals[1] = h.ids[last], h.vals[last]
		h.pos[h.ids[1]] = 1
		h.down(1)
	}
	h.ids, h.vals = h.ids[:last], h.vals[:last]
	h.state[id] = 2
	return id, v, true
}
func (h *Heap[T]) DecreaseKey(id int, v T) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case id < 0 || id >= len(h.state) || h.state[id] == 0:
		return ErrUnknown
	case h.state[id] == 2:
		return ErrGone
	case !h.less(v, h.vals[h.pos[id]]):
		return ErrNotSmaller
	}
	p := h.pos[id]
	h.vals[p], h.swaps = v, 0
	h.up(p)
	return nil
}
func (h *Heap[T]) Len() int               { h.mu.RLock(); defer h.mu.RUnlock(); return len(h.ids) - 1 }
func (h *Heap[T]) LastDecreaseSwaps() int { h.mu.RLock(); defer h.mu.RUnlock(); return h.swaps }
func (h *Heap[T]) swap(i, j int) {
	h.ids[i], h.ids[j] = h.ids[j], h.ids[i]
	h.vals[i], h.vals[j] = h.vals[j], h.vals[i]
	h.pos[h.ids[i]], h.pos[h.ids[j]] = i, j
}

func (h *Heap[T]) up(i int) {
	for p := i / 2; p > 0 && h.less(h.vals[i], h.vals[p]); p = i / 2 {
		h.swap(i, p)
		h.swaps++
		i = p
	}
}
func (h *Heap[T]) down(i int) {
	for n := len(h.ids) - 1; 2*i <= n; {
		c := 2 * i
		if r := c + 1; r <= n && h.less(h.vals[r], h.vals[c]) {
			c = r
		}
		if !h.less(h.vals[c], h.vals[i]) {
			return
		}
		h.swap(i, c)
		i = c
	}
}
