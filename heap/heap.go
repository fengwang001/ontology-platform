package heap

import "cmp"

var (
	ErrGone       = sentinel("heap: id has been popped")
	ErrUnknown    = sentinel("heap: unknown id")
	ErrNotSmaller = sentinel("heap: new value is not smaller")
)

type sentinel string

func (err sentinel) Error() string { return string(err) }

type Heap[T cmp.Ordered] struct {
	mu       sync.RWMutex
	entries  []entry[T]
	position []int
	nextID   int
	swaps    int
}

type entry[T cmp.Ordered] struct {
	id  int
	val T
}

func New[T cmp.Ordered]() *Heap[T] {
	return &Heap[T]{}
}

func (h *Heap[T]) Push(val T) int {
	h.mu.Lock()
	defer h.mu.Unlock()

	id := h.nextID
	h.nextID++
	h.position = append(h.position, len(h.entries))
	h.entries = append(h.entries, entry[T]{id: id, val: val})
	h.swaps = h.siftUp(len(h.entries) - 1)
	return id
}

func (h *Heap[T]) Peek() (T, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var zero T
	if len(h.entries) == 0 {
		return zero, false
	}
	return h.entries[0].val, true
}

func (h *Heap[T]) Pop() (id int, val T, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	var zero T
	if len(h.entries) == 0 {
		return 0, zero, false
	}
	root := h.entries[0]
	last := len(h.entries) - 1
	h.swap(0, last)
	h.entries = h.entries[:last]
	h.position[root.id] = -1
	if len(h.entries) > 0 {
		h.swaps = h.siftDown(0)
	}
	return root.id, root.val, true
}

func (h *Heap[T]) DecreaseKey(id int, newVal T) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if id < 0 || id >= len(h.position) {
		return ErrUnknown
	}
	index := h.position[id]
	if index < 0 {
		return ErrGone
	}
	current := h.entries[index].val
	if newVal >= current {
		return ErrNotSmaller
	}
	h.entries[index].val = newVal
	h.swaps = h.siftUp(index)
	return nil
}

func (h *Heap[T]) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.entries)
}

func (h *Heap[T]) LastSiftSwaps() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.swaps
}

func (h *Heap[T]) swap(i, j int) {
	h.entries[i], h.entries[j] = h.entries[j], h.entries[i]
	h.position[h.entries[i].id] = i
	h.position[h.entries[j].id] = j
}

func (h *Heap[T]) siftUp(index int) int {
	swaps := 0
	for index > 0 {
		parent := (index - 1) / 2
		if h.entries[index].val >= h.entries[parent].val {
			break
		}
		h.swap(index, parent)
		swaps++
		index = parent
	}
	return swaps
}

func (h *Heap[T]) siftDown(index int) int {
	swaps := 0
	for {
		left := index*2 + 1
		if left >= len(h.entries) {
			return swaps
		}
		smallest := left
		right := left + 1
		if right < len(h.entries) && h.entries[right].val < h.entries[left].val {
			smallest = right
		}
		if h.entries[index].val <= h.entries[smallest].val {
			return swaps
		}
		h.swap(index, smallest)
		swaps++
		index = smallest
	}
}
