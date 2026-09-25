// Package tenant holds a tenant's FIFO queue and its virtual-time ledger.
package tenant

import "ontology/task"

// MinCost is the lower bound charged to the virtual-time ledger per served
// task, so zero-cost tasks still advance virtual time and cannot monopolize
// the scheduler.
const MinCost int64 = 1

// Tenant is a tenant's task queue plus its scheduling ledger.
type Tenant struct {
	ID     string
	Weight float64
	VT     float64 // virtual time; smaller is served first
	q      []task.Task
}

// New returns an empty tenant with the given ID and weight.
func New(id string, weight float64) *Tenant {
	return &Tenant{ID: id, Weight: weight}
}

// Len reports the number of queued tasks.
func (t *Tenant) Len() int { return len(t.q) }

// Enqueue appends a task to the tail of the queue.
func (t *Tenant) Enqueue(tsk task.Task) { t.q = append(t.q, tsk) }

// Dequeue removes and returns the head task. Caller must ensure Len() > 0.
func (t *Tenant) Dequeue() task.Task {
	tsk := t.q[0]
	t.q = t.q[1:]
	return tsk
}

// Advance charges a served task of the given cost to the ledger:
// vt += max(cost, MinCost) / weight.
func (t *Tenant) Advance(cost int64) {
	if cost < MinCost {
		cost = MinCost
	}
	t.VT += float64(cost) / t.Weight
}
