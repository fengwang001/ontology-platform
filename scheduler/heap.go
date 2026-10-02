package scheduler

import "container/heap"

// readyHeap is an indexed max-heap ordered by effective priority (desc),
// ties broken by smaller seq. It lets the decision pick sigma in O(1).
type readyHeap []*task

func (h readyHeap) Len() int { return len(h) }

func (h readyHeap) Less(i, j int) bool {
	if h[i].e != h[j].e {
		return h[i].e > h[j].e
	}
	return h[i].seq < h[j].seq
}

func (h readyHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heap = i
	h[j].heap = j
}

func (h *readyHeap) Push(x any) {
	t := x.(*task)
	t.heap = len(*h)
	*h = append(*h, t)
}

func (h *readyHeap) Pop() any {
	old := *h
	n := len(old)
	t := old[n-1]
	old[n-1] = nil
	t.heap = -1
	*h = old[:n-1]
	return t
}

func (s *Scheduler) pushReady(t *task) {
	t.state = stateReady
	heap.Push(&s.ready, t)
	if t.bonus < s.bmax {
		at := t.epoch + s.w*(t.bonus+1)
		s.events[at] = append(s.events[at], &bonusEvent{t: t, epoch: t.epoch})
	}
}

func (s *Scheduler) popSigma() *task {
	return heap.Pop(&s.ready).(*task)
}

func (s *Scheduler) peekSigma() *task {
	if len(s.ready) == 0 {
		return nil
	}
	return s.ready[0]
}
