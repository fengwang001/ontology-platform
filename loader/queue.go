package loader

import "container/heap"

// reqHeap is a min-heap of requests ordered by registration sequence, so a
// paused request re-enters at its original registration position rather than
// at its pause time.
type reqHeap []*Request

func (h reqHeap) Len() int            { return len(h) }
func (h reqHeap) Less(i, j int) bool  { return h[i].seq < h[j].seq }
func (h reqHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *reqHeap) Push(x interface{}) { *h = append(*h, x.(*Request)) }

func (h *reqHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return item
}

// pendingQueue holds all schedulable (pending) requests. Selection cost is
// bounded by the number of distinct origins times the fixed number of
// priority levels, never by the total number of pending requests: within an
// (origin, level) bucket only the heap head is inspected.
type pendingQueue struct {
	byOrigin map[Origin]*[5]reqHeap
	order    [5][]Origin
	live     [5]map[Origin]bool
	steps    int
}

func newPendingQueue() *pendingQueue {
	q := &pendingQueue{byOrigin: make(map[Origin]*[5]reqHeap)}
	for i := range q.live {
		q.live[i] = make(map[Origin]bool)
	}
	return q
}

func (q *pendingQueue) heapFor(o Origin, lvl int) *reqHeap {
	hs := q.byOrigin[o]
	if hs == nil {
		hs = &[5]reqHeap{}
		q.byOrigin[o] = hs
	}
	return &hs[lvl]
}

// insert adds r to the bucket for its current effective priority.
func (q *pendingQueue) insert(r *Request) {
	lvl := int(r.prio)
	r.queued = true
	heap.Push(q.heapFor(r.origin, lvl), r)
	if !q.live[lvl][r.origin] {
		q.live[lvl][r.origin] = true
		q.order[lvl] = append(q.order[lvl], r.origin)
	}
}

// peekClean returns the live head of h, discarding stale entries left behind
// by cancellation or priority upgrades.
func peekClean(h *reqHeap, lvl int) *Request {
	for h.Len() > 0 {
		top := (*h)[0]
		if top.queued && int(top.prio) == lvl {
			return top
		}
		heap.Pop(h)
	}
	return nil
}

// selectNext returns the highest-priority, earliest-registered pending
// request whose origin is not full, or nil. The steps counter records how
// many origin buckets were inspected, for complexity verification.
func (q *pendingQueue) selectNext(full func(Origin) bool) *Request {
	q.steps = 0
	for lvl := int(PriorityHighest); lvl >= 0; lvl-- {
		var best *Request
		live := q.live[lvl]
		for _, o := range q.order[lvl] {
			q.steps++
			if !live[o] || full(o) {
				continue
			}
			head := peekClean(q.heapFor(o, lvl), lvl)
			if head == nil {
				delete(live, o)
				continue
			}
			if best == nil || head.seq < best.seq {
				best = head
			}
		}
		if best != nil {
			return best
		}
	}
	return nil
}

// dequeue removes r, which must be the live head of its bucket.
func (q *pendingQueue) dequeue(r *Request) {
	lvl := int(r.prio)
	h := q.heapFor(r.origin, lvl)
	if peekClean(h, lvl) == r {
		heap.Pop(h)
	}
	r.queued = false
	if h.Len() == 0 {
		delete(q.live[lvl], r.origin)
	}
}
