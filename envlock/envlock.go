// Package envlock provides a per-environment mutual-exclusion lock with
// a FIFO waiting queue. It is a pure data structure: it knows nothing
// about versions or approvals.
package envlock

// Lock holds at most one running request (0 means free) and a FIFO
// queue of waiting request IDs.
type Lock struct {
	running int64
	queue   []int64
}

// Running returns the ID currently holding the lock, or 0 if free.
func (l *Lock) Running() int64 { return l.running }

// Enqueue appends id to the tail of the waiting queue.
func (l *Lock) Enqueue(id int64) { l.queue = append(l.queue, id) }

// Remove deletes id from the waiting queue, reporting whether it was
// present. Removing a running ID is not possible here; use Release.
func (l *Lock) Remove(id int64) bool {
	for i, queued := range l.queue {
		if queued == id {
			l.queue = append(l.queue[:i], l.queue[i+1:]...)
			return true
		}
	}
	return false
}

// Peek returns the head of the waiting queue without removing it.
func (l *Lock) Peek() (int64, bool) {
	if len(l.queue) == 0 {
		return 0, false
	}
	return l.queue[0], true
}

// Pop drops the head of the waiting queue, if any.
func (l *Lock) Pop() {
	if len(l.queue) > 0 {
		l.queue = l.queue[1:]
	}
}

// Acquire pops the head of the queue and marks id as running. The
// caller must guarantee the lock is free and id is the head.
func (l *Lock) Acquire(id int64) {
	l.Pop()
	l.running = id
}

// Release frees the lock.
func (l *Lock) Release() { l.running = 0 }

// Queue returns a copy of the waiting queue, head first.
func (l *Lock) Queue() []int64 {
	out := make([]int64, len(l.queue))
	copy(out, l.queue)
	return out
}
