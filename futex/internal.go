package futex

import "container/heap"

// waiter is one node in exactly one per-address queue while a thread waits.
// The queue is a doubly linked list sorted by (prio, insertion order);
// gprev links the previous waiter of the same prio group so that the
// group's tail can be found in O(1) for requeue insertion.
type waiter struct {
	tid      int
	addr     int64
	bitset   uint32
	prio     int
	deadline int64
	seq      int64
	prev     *waiter // previous node in the address queue (nil at head)
	next     *waiter // next node in the address queue (nil at tail)
	gprev    *waiter // previous node of the same prio group (nil at group head)
	heapIdx  int     // index inside the global timeout heap; -1 when absent
}

// addrQueue holds the waiters of one address.
type addrQueue struct {
	head, tail *waiter
	// groupTail[p] is the last queued waiter with prio p.
	groupTail map[int]*waiter
}

func newAddrQueue() *addrQueue {
	return &addrQueue{groupTail: make(map[int]*waiter)}
}

// append inserts w at the tail of its prio group (i.e. after every waiter
// with smaller-or-equal prio, before any higher-prio node).
func (q *addrQueue) append(w *waiter) {
	w.prev, w.next, w.gprev = nil, nil, nil
	gt := q.groupTail[w.prio]
	if gt != nil {
		// Insert directly after the current tail of the same prio group.
		w.gprev = gt
		w.prev = gt
		w.next = gt.next
		if gt.next != nil {
			gt.next.prev = w
		} else {
			q.tail = w
		}
		gt.next = w
		q.groupTail[w.prio] = w
		return
	}
	// New prio group: find the first node with a greater prio.
	var mark *waiter
	for cur := q.head; cur != nil; cur = cur.next {
		if cur.prio > w.prio {
			mark = cur
			break
		}
	}
	switch {
	case mark == nil: // append at queue tail
		w.prev = q.tail
		if q.tail != nil {
			q.tail.next = w
		} else {
			q.head = w
		}
		q.tail = w
	case mark.prev == nil: // insert at queue head
		w.next = mark
		mark.prev = w
		q.head = w
	default:
		before := mark.prev
		w.prev, w.next = before, mark
		before.next, mark.prev = w, w
	}
	q.groupTail[w.prio] = w
}

// remove unlinks w, fixing group tails as needed.
func (q *addrQueue) remove(w *waiter) {
	if q.groupTail[w.prio] == w {
		q.groupTail[w.prio] = w.gprev
		if w.gprev == nil {
			delete(q.groupTail, w.prio)
		}
	}
	if w.prev != nil {
		w.prev.next = w.next
	} else {
		q.head = w.next
	}
	if w.next != nil {
		w.next.prev = w.prev
	} else {
		q.tail = w.prev
	}
	w.prev, w.next, w.gprev = nil, nil, nil
}

func (q *addrQueue) empty() bool { return q.head == nil }

// tids returns the queue contents as a tid slice in queue order.
func (q *addrQueue) tids() []int {
	var out []int
	for cur := q.head; cur != nil; cur = cur.next {
		out = append(out, cur.tid)
	}
	return out
}

// timeoutHeap is a min-heap keyed by (deadline, seq).
type timeoutHeap []*waiter

func (h timeoutHeap) Len() int { return len(h) }

func (h timeoutHeap) Less(i, j int) bool {
	if h[i].deadline != h[j].deadline {
		return h[i].deadline < h[j].deadline
	}
	return h[i].seq < h[j].seq
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

var _ heap.Interface = (*timeoutHeap)(nil)
