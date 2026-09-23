// Package waitq is a FIFO queue of waiters with O(1) cancellation.
// Cancellation removes the waiter's own node directly (no scan);
// dequeue is the commit point: once dequeued a waiter can no longer
// cancel and must accept the delivery buffered in Ch.
package waitq

import "container/list"

// Waiter is one queued acquirer. Ch has capacity 1: the pool delivers
// exactly one value after dequeuing, without ever blocking.
type Waiter[T any] struct {
	Ch   chan T
	elem *list.Element
}

func NewWaiter[T any]() *Waiter[T] { return &Waiter[T]{Ch: make(chan T, 1)} }

// Queue is not safe for concurrent use; the caller serializes access.
// checked counts the nodes inspected by the most recent operation.
type Queue[T any] struct {
	l       list.List
	n       int
	checked int
}

func (q *Queue[T]) Len() int { return q.n }

func (q *Queue[T]) Enqueue(w *Waiter[T]) { w.elem = q.l.PushBack(w); q.n++ }

// Cancel removes w if it is still queued and reports whether it was.
// It inspects only w's own node. A false result means w was already
// dequeued and a delivery is in flight (or buffered) on w.Ch.
func (q *Queue[T]) Cancel(w *Waiter[T]) bool {
	q.checked = 1
	if w.elem == nil {
		return false
	}
	q.l.Remove(w.elem)
	w.elem = nil
	q.n--
	return true
}

// Dequeue pops the oldest waiter. Cancelled waiters are removed eagerly
// by Cancel, so the front is always live and Dequeue inspects one node.
func (q *Queue[T]) Dequeue() (*Waiter[T], bool) {
	q.checked = 0
	e := q.l.Front()
	if e == nil {
		return nil, false
	}
	q.checked = 1
	q.l.Remove(e)
	w := e.Value.(*Waiter[T])
	w.elem = nil
	q.n--
	return w, true
}
