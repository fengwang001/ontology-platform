// Package tenant holds a per-tenant FIFO queue and its virtual-time ledger.
package tenant

import (
	"errors"
	"math"

	"ontology/task"
)

// ErrBadWeight is returned when a weight is not a finite positive number.
var ErrBadWeight = errors.New("tenant: weight must be a finite positive number")

// MinStep is the smallest virtual-time charge per dispatch, so zero-cost
// tasks cannot pin a tenant's virtual time and monopolize the scheduler.
const MinStep = 1.0

// Queue is one tenant's FIFO task queue together with its weight and vt.
type Queue struct {
	id     string
	weight float64
	vt     float64
	tasks  []task.Task
	seq    int64
	active bool
}

// New creates a tenant queue with the given positive finite weight.
func New(id string, weight float64) (*Queue, error) {
	if !(weight > 0) || math.IsInf(weight, 0) {
		return nil, ErrBadWeight
	}
	return &Queue{id: id, weight: weight}, nil
}

func (q *Queue) ID() string     { return q.id }
func (q *Queue) Weight() float64 { return q.weight }
func (q *Queue) VT() float64    { return q.vt }
func (q *Queue) Len() int       { return len(q.tasks) }

// Active reports whether the queue is currently installed in the heap.
func (q *Queue) Active() bool { return q.active }

// SetActive flips heap membership; sched owns the invariant.
func (q *Queue) SetActive(a bool) { q.active = a }

// Bump raises vt to at least now (system virtual time) on empty -> nonempty.
func (q *Queue) Bump(now float64) {
	if now > q.vt {
		q.vt = now
	}
}

// Enqueue appends a cost task. When the queue was empty the tenant is
// re-entering service, so its vt is raised to the current system virtual time.
func (q *Queue) Enqueue(cost int64, now float64) (task.Task, error) {
	if err := task.ValidateCost(cost); err != nil {
		return task.Task{}, err
	}
	if len(q.tasks) == 0 {
		q.Bump(now)
	}
	t := task.Task{Tenant: q.id, Cost: cost, Seq: q.seq}
	q.seq++
	q.tasks = append(q.tasks, t)
	return t, nil
}

// Dequeue removes and returns the head task.
func (q *Queue) Dequeue() (task.Task, bool) {
	if len(q.tasks) == 0 {
		return task.Task{}, false
	}
	t := q.tasks[0]
	q.tasks = q.tasks[1:]
	return t, true
}

// Advance charges one dispatched task of real cost c against the tenant:
// vt += max(c, MinStep) / weight.
func (q *Queue) Advance(c int64) {
	step := float64(c)
	if step < MinStep {
		step = MinStep
	}
	q.vt += step / q.weight
}
