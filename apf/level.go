package apf

import (
	"container/list"
	"time"
)

// waiter is a queued request.
type waiter struct {
	req        Request
	enqueuedAt time.Time
	deadline   time.Time
	seq        uint64
	flow       *flowState
	elem       *list.Element
	level      *levelState
	ticket     *Ticket
	queued     bool
}

// flowState holds the FIFO queue of one flow. A flow is linked into
// levelState.flowList exactly while it has waiters.
type flowState struct {
	key   string
	queue *list.List // of *waiter, FIFO
	elem  *list.Element
}

// levelState is the mutable runtime state of one limited priority level.
//
// Concurrency costs are independent of the number of flows and of the number
// of waiting requests: flows live in a map plus an intrusive list, the
// round-robin cursor is a single list element, and timeout eviction uses a
// binary heap.
type levelState struct {
	name         string
	nominal      int
	occupied     int
	queueLimit   int
	queueTimeout time.Duration

	flows      map[string]*flowState
	flowOrder  *list.List // of *flowState, non-empty flows in became-nonempty order
	cursor     *list.Element
	lastServed string // flow key of the most recently served flow ("" if none)

	waiters int
	heap    waiterHeap
}

func newLevelState(name string, nominal, queueLimit int, queueTimeout time.Duration, probe *uint64) *levelState {
	return &levelState{
		name:         name,
		nominal:      nominal,
		queueLimit:   queueLimit,
		queueTimeout: queueTimeout,
		flows:        make(map[string]*flowState),
		flowOrder:    list.New(),
		heap:         waiterHeap{probe: probe},
	}
}

// removeFlowElem unlinks a flow that no longer has waiters, keeping the
// round-robin cursor anchored at the removed element's predecessor so that
// "the first flow after the last served one" stays well defined.
func (ls *levelState) removeFlowElem(el *list.Element) {
	if ls.cursor == el {
		ls.cursor = el.Prev()
	}
	ls.flowOrder.Remove(el)
}

// nextFlow returns the element of the flow to serve next: the first
// non-empty flow after the last served one in became-nonempty order,
// wrapping around.
func (ls *levelState) nextFlow() *list.Element {
	if ls.cursor == nil {
		return ls.flowOrder.Front()
	}
	if next := ls.cursor.Next(); next != nil {
		return next
	}
	return ls.flowOrder.Front()
}

// enqueue appends w to its flow, creating and linking the flow when it
// transitions from empty to non-empty (which appends it at the end of the
// rotation order).
func (ls *levelState) enqueue(w *waiter) {
	fs, ok := ls.flows[w.flow.key]
	if !ok {
		fs = &flowState{key: w.flow.key, queue: list.New()}
		ls.flows[fs.key] = fs
	}
	if fs.queue.Len() == 0 {
		fs.elem = ls.flowOrder.PushBack(fs)
		// The flow re-joins at the end of the rotation order. If it is the
		// last served flow, the round-robin cursor must follow it there:
		// "the next flow after the last served one" is defined by flow
		// identity, not by list position.
		if ls.lastServed == fs.key {
			ls.cursor = fs.elem
		}
	}
	w.flow = fs
	w.queued = true
	w.elem = fs.queue.PushBack(w)
	ls.waiters++
	ls.heap.push(w)
}

// unlink removes w from its flow's queue; the caller decides what happens
// next (grant, timeout, rejection).
func (ls *levelState) unlink(w *waiter, el *list.Element) {
	fs := w.flow
	fs.queue.Remove(el)
	w.queued = false
	ls.waiters--
	if fs.queue.Len() == 0 {
		ls.removeFlowElem(fs.elem)
		delete(ls.flows, fs.key)
	}
}

// dispatch grants as many queued requests as possible. Only the head of the
// round-robin-selected flow is considered; if it does not fit, the whole
// level stops (head-of-line blocking). Returns the granted waiters.
func (ls *levelState) dispatch() []*waiter {
	var granted []*waiter
	for ls.flowOrder.Len() > 0 {
		el := ls.nextFlow()
		fs := el.Value.(*flowState)
		headEl := fs.queue.Front()
		w := headEl.Value.(*waiter)
		if ls.occupied+w.req.Seats > ls.nominal {
			break
		}
		ls.cursor = el
		ls.lastServed = fs.key
		ls.unlink(w, headEl)
		ls.occupied += w.req.Seats
		granted = append(granted, w)
	}
	return granted
}

// evict removes all waiters whose deadline is at or before t (left-closed:
// waiting exactly the queue timeout already counts as timed out). Each
// eviction costs O(log n) in the number of waiters.
func (ls *levelState) evict(t time.Time) []*waiter {
	var expired []*waiter
	for {
		w := ls.heap.peek()
		if w == nil || w.deadline.After(t) {
			break
		}
		ls.heap.pop()
		ls.unlink(w, w.elem)
		expired = append(expired, w)
	}
	return expired
}
