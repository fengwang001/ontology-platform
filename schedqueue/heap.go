package schedqueue

import "container/heap"

// activeHeap orders by prio desc, then t0 asc, then id bytewise asc.
type activeHeap []*pod

func (h activeHeap) Len() int { return len(h) }

func (h activeHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.prio != b.prio {
		return a.prio > b.prio
	}
	if a.t0 != b.t0 {
		return a.t0 < b.t0
	}
	return a.id < b.id
}

func (h activeHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *activeHeap) Push(x any) {
	p := x.(*pod)
	p.index = len(*h)
	*h = append(*h, p)
}

func (h *activeHeap) Pop() any {
	old := *h
	n := len(old)
	p := old[n-1]
	old[n-1] = nil
	p.index = -1
	*h = old[:n-1]
	return p
}

// backoffHeap orders by exp asc, then id bytewise asc for determinism.
type backoffHeap []*pod

func (h backoffHeap) Len() int { return len(h) }

func (h backoffHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.exp != b.exp {
		return a.exp < b.exp
	}
	return a.id < b.id
}

func (h backoffHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *backoffHeap) Push(x any) {
	p := x.(*pod)
	p.index = len(*h)
	*h = append(*h, p)
}

func (h *backoffHeap) Pop() any {
	old := *h
	n := len(old)
	p := old[n-1]
	old[n-1] = nil
	p.index = -1
	*h = old[:n-1]
	return p
}

func (q *Queue) pushActive(p *pod) {
	p.st = stateActive
	heap.Push(&q.active, p)
}

func heapRemoveActive(q *Queue, p *pod) {
	heap.Remove(&q.active, p.index)
}

func (q *Queue) pushBackoff(p *pod) {
	p.st = stateBackoff
	heap.Push(&q.backoff, p)
}

func heapRemoveBackoff(q *Queue, p *pod) {
	heap.Remove(&q.backoff, p.index)
}

func (q *Queue) pushUnsched(p *pod, now int64) {
	p.st = stateUnschedulable
	p.parked = now
	q.unsched[p.id] = p
}

// removeFromLocation takes p out of whatever queue currently holds it.
func (q *Queue) removeFromLocation(p *pod) {
	switch p.st {
	case stateActive:
		heap.Remove(&q.active, p.index)
	case stateBackoff:
		heap.Remove(&q.backoff, p.index)
	case stateUnschedulable:
		delete(q.unsched, p.id)
	case stateInFlight:
		delete(q.inFlight, p.id)
	}
}
