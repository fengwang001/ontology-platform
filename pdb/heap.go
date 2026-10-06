package pdb

import "container/heap"

func (h deadlineHeap) Len() int           { return len(h) }
func (h deadlineHeap) Less(i, j int) bool { return h[i].deadline.Before(h[j].deadline) }
func (h deadlineHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}

func (h *deadlineHeap) Push(x any) {
	e := x.(*eviction)
	e.heapIndex = len(*h)
	*h = append(*h, e)
}

func (h *deadlineHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.heapIndex = -1
	*h = old[:n-1]
	return e
}

// heapPush/Pop/Remove are thin wrappers so call sites read clearly.
func heapPush(h *deadlineHeap, e *eviction) { heap.Push(h, e) }
func heapPop(h *deadlineHeap) *eviction     { return heap.Pop(h).(*eviction) }
func heapRemove(h *deadlineHeap, e *eviction) {
	if e.heapIndex >= 0 {
		heap.Remove(h, e.heapIndex)
	}
}
