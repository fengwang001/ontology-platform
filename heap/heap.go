package heap

import (
	"cmp"
	"sync"
)

const (
	ErrGone       = sentinel("heap: id has already been popped")
	ErrUnknown    = sentinel("heap: unknown id")
	ErrNotSmaller = sentinel("heap: new value is not smaller")
)

type sentinel string

func (e sentinel) Error() string { return string(e) }

type Entry[T cmp.Ordered] struct {
	ID  int
	Val T
}

type Heap[T cmp.Ordered] struct {
	sync.RWMutex
	a     []Entry[T]
	pos   map[int]int
	next  int
	swaps int
}

func New[T cmp.Ordered]() *Heap[T] { return &Heap[T]{pos: map[int]int{}} }

func (h *Heap[T]) Push(v T) int {
	h.Lock()
	defer h.Unlock()
	id := h.next
	h.next++
	h.a = append(h.a, Entry[T]{id, v})
	h.pos[id] = len(h.a) - 1
	h.swaps = 0
	h.up(len(h.a) - 1)
	return id
}

func (h *Heap[T]) Peek() (Entry[T], bool) {
	h.RLock()
	defer h.RUnlock()
	if len(h.a) == 0 {
		return Entry[T]{}, false
	}
	return h.a[0], true
}

func (h *Heap[T]) Pop() (Entry[T], bool) {
	h.Lock()
	defer h.Unlock()
	if len(h.a) == 0 {
		return Entry[T]{}, false
	}
	root, last := h.a[0], len(h.a)-1
	h.pos[root.ID] = -1
	if last > 0 {
		h.a[0] = h.a[last]
		h.pos[h.a[0].ID] = 0
		h.swaps = 0
		h.down(0)
	}
	h.a = h.a[:last]
	return root, true
}

func (h *Heap[T]) DecreaseKey(id int, v T) error {
	h.Lock()
	defer h.Unlock()
	i, ok := h.pos[id]
	if !ok {
		return ErrUnknown
	}
	if i < 0 {
		return ErrGone
	}
	if v >= h.a[i].Val {
		return ErrNotSmaller
	}
	h.a[i].Val, h.swaps = v, 0
	h.up(i)
	return nil
}

func (h *Heap[T]) Len() int       { h.RLock(); defer h.RUnlock(); return len(h.a) }
func (h *Heap[T]) LastSwaps() int { h.RLock(); defer h.RUnlock(); return h.swaps }

func (h *Heap[T]) up(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if h.a[i].Val >= h.a[p].Val {
			return
		}
		h.xchg(i, p)
		i = p
	}
}

func (h *Heap[T]) down(i int) {
	for {
		l, s := 2*i+1, 2*i+1
		if l >= len(h.a) {
			return
		}
		if r := l + 1; r < len(h.a) && h.a[r].Val < h.a[l].Val {
			s = r
		}
		if h.a[i].Val <= h.a[s].Val {
			return
		}
		h.xchg(i, s)
		i = s
	}
}

func (h *Heap[T]) xchg(a, b int) {
	h.a[a], h.a[b] = h.a[b], h.a[a]
	h.pos[h.a[a].ID], h.pos[h.a[b].ID], h.swaps = a, b, h.swaps+1
}
