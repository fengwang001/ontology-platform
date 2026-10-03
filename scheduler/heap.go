package scheduler

type minHeap []*batch

func (h minHeap) Len() int { return len(h) }

func (h minHeap) Less(i, j int) bool {
	if h[i].fireAt != h[j].fireAt {
		return h[i].fireAt < h[j].fireAt
	}
	return h[i].seq < h[j].seq
}

func (h minHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *minHeap) Push(value any) {
	*h = append(*h, value.(*batch))
}

func (h *minHeap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	old[last] = nil
	*h = old[:last]
	return value
}

func (s *Scheduler) heap() *minHeap {
	return (*minHeap)(&s.pending)
}
