package futex

import "container/list"

// addrQueue is the wait queue of one address: one FIFO list per priority
// level. Queue order is prio ascending, and FIFO within the same prio.
// Insertion always appends to the tail of the waiter's prio group.
type addrQueue struct {
	groups [prioLevels]*list.List
	length int
}

// elemOf maps a list element back to its waiter.
type queueEntry struct {
	w     *waiter
	group int // prio level of the list holding this entry
}

func (q *addrQueue) pushBack(w *waiter) {
	g := q.groups[w.prio]
	if g == nil {
		g = list.New()
		q.groups[w.prio] = g
	}
	e := g.PushBack(&queueEntry{w: w, group: w.prio})
	w.elem = e
	w.queue = q
	q.length++
}

// remove detaches w from whichever group list holds it. O(1).
func (q *addrQueue) remove(w *waiter) {
	if w.elem == nil {
		return
	}
	q.groups[w.prio].Remove(w.elem)
	w.elem = nil
	w.queue = nil
	q.length--
}

// forEach visits waiters in queue order until fn returns false.
func (q *addrQueue) forEach(fn func(w *waiter) bool) {
	for p := 0; p < prioLevels; p++ {
		g := q.groups[p]
		if g == nil {
			continue
		}
		for e := g.Front(); e != nil; {
			next := e.Next()
			if !fn(e.Value.(*queueEntry).w) {
				return
			}
			e = next
		}
	}
}

// popFront removes and returns the head waiter, or nil when empty.
func (q *addrQueue) popFront() *waiter {
	for p := 0; p < prioLevels; p++ {
		g := q.groups[p]
		if g == nil || g.Len() == 0 {
			continue
		}
		e := g.Front()
		w := e.Value.(*queueEntry).w
		g.Remove(e)
		w.elem = nil
		w.queue = nil
		q.length--
		return w
	}
	return nil
}

// tids lists queued thread ids in queue order.
func (q *addrQueue) tids() []int64 {
	out := make([]int64, 0, q.length)
	q.forEach(func(w *waiter) bool {
		out = append(out, w.tid)
		return true
	})
	return out
}
