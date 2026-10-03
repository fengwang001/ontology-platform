package futex

import "container/heap"

// timeoutHeap is a min-heap of waiters with nonzero deadlines, ordered by
// (deadline, seq). Entries are removed eagerly when a waiter leaves its
// queue, so Advance only inspects waiters that actually time out plus at
// most one extra heap top.
type timeoutHeap []*waiter

func (h timeoutHeap) Len() int { return len(h) }

func (h timeoutHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.deadline != b.deadline {
		return a.deadline < b.deadline
	}
	return a.seq < b.seq
}

func (h timeoutHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIdx = i
	h[j].heapIdx = j
}

func (h *timeoutHeap) Push(x any) {
	w := x.(*waiter)
	w.heapIdx = len(*h)
	*h = append(*h, w)
}

func (h *timeoutHeap) Pop() any {
	old := *h
	n := len(old)
	w := old[n-1]
	old[n-1] = nil
	w.heapIdx = -1
	*h = old[:n-1]
	return w
}

// track inserts w into the timeout heap; w must have a nonzero deadline.
func (f *Futex) track(w *waiter) {
	heap.Push(&f.deadlines, w)
}

// untrack removes w from the timeout heap if present.
func (f *Futex) untrack(w *waiter) {
	if w.heapIdx >= 0 {
		heap.Remove(&f.deadlines, w.heapIdx)
	}
}
