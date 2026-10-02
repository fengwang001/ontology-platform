package ontology

import "container/heap"

type activeIndex struct {
	heaps []*timeHeap
}

func newActiveIndex(n int) *activeIndex {
	heaps := make([]*timeHeap, n)
	for i := range heaps {
		heaps[i] = &timeHeap{}
	}
	return &activeIndex{heaps: heaps}
}

func (index *activeIndex) add(position int, time int64) {
	heap.Push(index.heaps[position], time)
}

func (index *activeIndex) minimum(position int, isActive func(int64) bool) (int64, bool) {
	values := index.heaps[position]
	for values.Len() > 0 {
		time := (*values)[0]
		if isActive(time) {
			return time, true
		}
		heap.Pop(values)
	}
	return -1, false
}

type timeHeap []int64

func (h timeHeap) Len() int {
	return len(h)
}

func (h timeHeap) Less(i, j int) bool {
	return h[i] < h[j]
}

func (h timeHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *timeHeap) Push(value any) {
	*h = append(*h, value.(int64))
}

func (h *timeHeap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	*h = old[:last]
	return value
}
