// Package level implements a single FIFO queue for one MLFQ priority level.
package level

// Queue is a first-in-first-out queue of job ids.
type Queue struct {
	items []int
	head  int
}

// Len reports the number of queued ids.
func (q *Queue) Len() int { return len(q.items) - q.head }

// Push appends id to the tail.
func (q *Queue) Push(id int) { q.items = append(q.items, id) }

// Front returns the head id without removing it.
func (q *Queue) Front() (int, bool) {
	if q.Len() == 0 {
		return 0, false
	}
	return q.items[q.head], true
}

// Pop removes and returns the head id.
func (q *Queue) Pop() (int, bool) {
	id, ok := q.Front()
	if !ok {
		return 0, false
	}
	q.head++
	if q.head == len(q.items) {
		q.items = q.items[:0]
		q.head = 0
	}
	return id, true
}
