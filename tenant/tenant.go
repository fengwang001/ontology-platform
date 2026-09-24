// Package tenant holds per-tenant FIFO queues and virtual-time ledgers.
package tenant

import (
	"math"

	"ontology/task"
)

// MinCost is the billable floor: zero-cost tasks still advance virtual time so
// they cannot monopolize the scheduler (DESIGN.md section 5).
const MinCost = 1e-9

// Queue is one tenant's backlog and its virtual-time account. All access is
// mediated by the scheduler lock.
type Queue struct {
	ID     string
	Weight float64
	VT     float64
	q      []task.Task
}

// NewQueue builds a queue with weight validated via task.CheckWeight.
func NewQueue(id string, weight float64) (*Queue, error) {
	if err := task.CheckWeight(weight); err != nil {
		return nil, err
	}
	return &Queue{ID: id, Weight: weight}, nil
}

// Len reports the queued task count.
func (q *Queue) Len() int { return len(q.q) }

// Empty reports whether the queue has no tasks.
func (q *Queue) Empty() bool { return len(q.q) == 0 }

// Push appends a task.
func (q *Queue) Push(t task.Task) { q.q = append(q.q, t) }

// Pop removes and returns the head task; it panics on an empty queue.
func (q *Queue) Pop() task.Task {
	t := q.q[0]
	n := len(q.q)
	copy(q.q, q.q[1:])
	q.q[n-1] = task.Task{}
	q.q = q.q[:n-1]
	return t
}

// Raise lifts virtual time to baseline when the queue re-enters service
// (DESIGN.md section 3).
func (q *Queue) Raise(baseline float64) {
	if baseline > q.VT {
		q.VT = baseline
	}
}

// Charge advances virtual time by the floored billable cost over weight.
func (q *Queue) Charge(cost float64) float64 {
	c := cost
	if c < MinCost {
		c = MinCost
	}
	inc := c / q.Weight
	q.VT += inc
	return q.VT
}

// Finite reports whether the ledger holds a usable (non-NaN/Inf) value.
func (q *Queue) Finite() bool { return !math.IsNaN(q.VT) && !math.IsInf(q.VT, 0) }
