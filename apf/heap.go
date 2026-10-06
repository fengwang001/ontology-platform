package apf

import "container/heap"

// waiterHeap is a min-heap of waiters ordered by (deadline, seq). It gives
// O(log n) timeout eviction instead of scanning the whole queue. Stale
// entries (waiters already dequeued or evicted) are discarded lazily on pop.
type waiterHeap struct {
	items []*waiter
	probe *uint64
}

func (h waiterHeap) Len() int { return len(h.items) }

func (h waiterHeap) Less(i, j int) bool {
	if h.probe != nil {
		*h.probe++
	}
	a, b := h.items[i], h.items[j]
	if !a.deadline.Equal(b.deadline) {
		return a.deadline.Before(b.deadline)
	}
	return a.seq < b.seq
}

func (h waiterHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *waiterHeap) Push(x any) { h.items = append(h.items, x.(*waiter)) }

func (h *waiterHeap) Pop() any {
	old := h.items
	n := len(old)
	item := old[n-1]
	h.items = old[:n-1]
	return item
}

func (h *waiterHeap) push(w *waiter) { heap.Push(h, w) }

// peek returns the earliest still-queued waiter, discarding stale entries.
func (h *waiterHeap) peek() *waiter {
	for len(h.items) > 0 {
		w := h.items[0]
		if w.queued {
			return w
		}
		heap.Pop(h)
	}
	return nil
}

// pop removes and returns the earliest still-queued waiter.
func (h *waiterHeap) pop() *waiter {
	if w := h.peek(); w != nil {
		heap.Pop(h)
		return w
	}
	return nil
}
