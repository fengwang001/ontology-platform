// Package heap implements a generic indexed binary min-heap supporting
// decrease-key. IDs returned by Push remain valid for later lookup even
// after the element moves inside the heap.
package heap

import (
	"cmp"
	"errors"
	"sync"
)

var (
	// ErrUnknown is returned when an ID was never issued by Push.
	ErrUnknown = errors.New("heap: unknown id")
	// ErrGone is returned when an ID has already been popped.
	ErrGone = errors.New("heap: element already popped")
	// ErrNotSmaller is returned when the new key is not strictly smaller.
	ErrNotSmaller = errors.New("heap: new value is not smaller")
)

// Heap is an indexed binary min-heap. The zero value is not usable;
// create one with New.
type Heap[T cmp.Ordered] struct {
	mu     sync.Mutex
	nodes  []entry[T] // 1-indexed storage
	pos    map[int]int
	nextID int
	swaps  int // swaps performed by the most recent adjustment
}

type entry[T any] struct {
	id  int
	val T
}

// New returns an empty indexed heap.
func New[T cmp.Ordered]() *Heap[T] {
	return &Heap[T]{
		nodes:  []entry[T]{{}},
		pos:    make(map[int]int),
		nextID: 1,
	}
}

// Len returns the number of elements currently in the heap.
func (h *Heap[T]) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.nodes) - 1
}

// LastSwaps reports the swap count of the most recent Push, Pop or
// DecreaseKey adjustment (0 after the last operation needed no swaps).
func (h *Heap[T]) LastSwaps() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.swaps
}

// Push inserts val and returns its stable ID.
func (h *Heap[T]) Push(val T) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.nextID
	h.nextID++
	h.nodes = append(h.nodes, entry[T]{id: id, val: val})
	h.pos[id] = len(h.nodes) - 1
	h.swaps = 0
	h.siftUp(len(h.nodes)-1, true)
	return id
}

// Peek returns the smallest key without removing it.
func (h *Heap[T]) Peek() (T, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var zero T
	if len(h.nodes) == 1 {
		return zero, false
	}
	return h.nodes[1].val, true
}

// Pop removes and returns the smallest key.
func (h *Heap[T]) Pop() (T, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var zero T
	if len(h.nodes) == 1 {
		return zero, false
	}
	top := h.nodes[1]
	last := len(h.nodes) - 1
	h.nodes[1] = h.nodes[last]
	h.pos[h.nodes[1].id] = 1
	h.nodes = h.nodes[:last]
	delete(h.pos, top.id)
	h.swaps = 0
	if last > 1 {
		h.siftDown(1, true)
	}
	return top.val, true
}

// DecreaseKey lowers the key of id to newVal. It succeeds only when
	newVal is strictly smaller than the current key; afterwards the
	element can only move upward, so only sift-up is performed.
func (h *Heap[T]) DecreaseKey(id int, newVal T) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.pos[id]
	if !ok {
		if 1 <= id && id < h.nextID {
			return ErrGone
		}
		return ErrUnknown
	}
	if !(newVal < h.nodes[p].val) {
		return ErrNotSmaller
	}
	h.nodes[p].val = newVal
	h.swaps = 0
	h.siftUp(p, true)
	return nil
}

func (h *Heap[T]) less(i, j int) bool { return h.nodes[i].val < h.nodes[j].val }

func (h *Heap[T]) swap(i, j int, count bool) {
	h.nodes[i], h.nodes[j] = h.nodes[j], h.nodes[i]
	h.pos[h.nodes[i].id], h.pos[h.nodes[j].id] = i, j
	if count {
		h.swaps++
	}
}

func (h *Heap[T]) siftUp(i int, count bool) {
	for i > 1 && h.less(i, i/2) {
		h.swap(i, i/2, count)
		i /= 2
	}
}

func (h *Heap[T]) siftDown(i int, count bool) {
	for {
		left := 2 * i
		if left >= len(h.nodes) {
			return
		}
		child := left
		if right := left + 1; right < len(h.nodes) && h.less(right, left) {
			child = right
		}
		if !h.less(child, i) {
			return
		}
		h.swap(i, child, count)
		i = child
	}
}
