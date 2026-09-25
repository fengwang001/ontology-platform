// Package admit adds per-tenant queue-cap backpressure on top of sched.
package admit

import (
	"errors"
	"sync"

	"ontology/sched"
	"ontology/task"
)

// ErrQueueFull is returned when a tenant's queue reaches the cap.
// Other tenants are unaffected.
var ErrQueueFull = errors.New("admit: tenant queue full")

// Queue wraps a Scheduler with a per-tenant queue cap.
type Queue struct {
	mu  sync.Mutex
	sch *sched.Scheduler
	cap int
	seq uint64
}

// New creates a Queue where each tenant may queue at most cap tasks.
func New(sch *sched.Scheduler, cap int) *Queue {
	return &Queue{sch: sch, cap: cap}
}

// Submit assigns the next sequence number and enqueues the task, or
// returns ErrQueueFull if the tenant's queue is at the cap. The check
// and the enqueue are atomic with respect to other Submit calls.
func (q *Queue) Submit(t task.Task) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.sch.QueueLen(t.Tenant) >= q.cap {
		return ErrQueueFull
	}
	t.Seq = q.seq
	q.seq++
	return q.sch.Submit(t)
}

// Next dequeues the next task from the underlying scheduler.
func (q *Queue) Next() (task.Task, bool) {
	return q.sch.Next()
}
