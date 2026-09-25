// Package tenant holds a tenant's task queue and virtual-time ledger.
package tenant

import "ontology/task"

// MinCost is the minimum billed cost per executed task. It keeps the
// virtual time advancing even for zero-cost tasks, so a tenant cannot
// monopolize the scheduler with free work.
const MinCost = 1.0

// Tenant is a tenant's queue plus its virtual-time ledger. It is not
// safe for concurrent use; the sched package serializes all access.
type Tenant struct {
	// ID is the tenant identifier, used to break virtual-time ties.
	ID string
	// Weight is the scheduling weight; must be positive.
	Weight float64
	// VT is the virtual time: accumulated billed cost divided by weight.
	VT float64

	queue []task.Task
}

// New creates a tenant with the given ID and weight.
func New(id string, weight float64) *Tenant {
	return &Tenant{ID: id, Weight: weight}
}

// Enqueue appends a task to the tenant's queue.
func (t *Tenant) Enqueue(tk task.Task) {
	t.queue = append(t.queue, tk)
}

// Dequeue removes and returns the head task. Callers must check Len first.
func (t *Tenant) Dequeue() task.Task {
	tk := t.queue[0]
	t.queue = t.queue[1:]
	return tk
}

// Len reports the number of queued tasks.
func (t *Tenant) Len() int {
	return len(t.queue)
}

// Advance bills one executed task of the given cost to the ledger:
// VT grows by cost/weight so that long-run service is proportional to
// weight. Costs below MinCost are billed as MinCost.
func (t *Tenant) Advance(cost float64) {
	if cost < MinCost {
		cost = MinCost
	}
	t.VT += cost / t.Weight
}
