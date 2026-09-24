// Package waitq is a FIFO queue of waiters with O(1) cancellation.
// It is not goroutine-safe; callers (package sem) hold the lock.
package waitq

import "container/list"

// Waiter is one queued acquisition. Grant is buffered (cap 1); the
// owner sets Granted and sends on Grant inside the same critical
// section that moves the quota.
type Waiter struct {
	N       int64
	Granted bool
	Grant   chan struct{}
	elem    *list.Element
}

// Queue is a FIFO of waiters. lastCancelChecked counts the nodes a
// Remove inspected; it must stay a small constant (no queue scan).
type Queue struct {
	l                 *list.List
	lastCancelChecked int
}

// New returns an empty Queue.
func New() *Queue { return &Queue{l: list.New()} }

// Len returns the number of queued waiters.
func (q *Queue) Len() int { return q.l.Len() }

// Push appends w at the tail and records its element handle.
func (q *Queue) Push(w *Waiter) {
	w.Grant = make(chan struct{}, 1)
	w.elem = q.l.PushBack(w)
}

// Front returns the head waiter, or nil.
func (q *Queue) Front() *Waiter {
	if e := q.l.Front(); e != nil {
		return e.Value.(*Waiter)
	}
	return nil
}

// Pop removes and returns the head waiter, or nil.
func (q *Queue) Pop() *Waiter {
	e := q.l.Front()
	if e == nil {
		return nil
	}
	q.l.Remove(e)
	return e.Value.(*Waiter)
}

// Remove cancels w in O(1) via its recorded element handle; it never
// scans the queue, so exactly one node is touched.
func (q *Queue) Remove(w *Waiter) {
	q.lastCancelChecked = 1
	q.l.Remove(w.elem)
	w.elem = nil
}

// LastCancelChecked returns the node count of the last Remove.
func (q *Queue) LastCancelChecked() int { return q.lastCancelChecked }
