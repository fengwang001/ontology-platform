// Package tenant holds a tenant's queue and virtual-time ledger.
package tenant

import "ontology/task"

// MinCost is the lower bound applied to task cost so that virtual time
// always advances and a stream of zero-cost tasks cannot monopolize.
const MinCost = 1.0

// Tenant is a tenant's queue plus its virtual-time ledger.
type Tenant struct {
	ID     string
	Weight float64
	vt     float64
	queue  []task.Task
}

// New creates a tenant with the given ID and positive weight.
func New(id string, weight float64) *Tenant {
	return &Tenant{ID: id, Weight: weight}
}

// VT returns the tenant's current virtual time.
func (t *Tenant) VT() float64 { return t.vt }

// Lift raises vt to at least v; used when an idle tenant rejoins so it
// cannot monopolize the scheduler while catching up (see DESIGN.md §2).
func (t *Tenant) Lift(v float64) {
	if t.vt < v {
		t.vt = v
	}
}

// SetVT forces vt; used by experiments that disable lifting.
func (t *Tenant) SetVT(v float64) { t.vt = v }

// Enqueue appends a task to the tenant's queue.
func (t *Tenant) Enqueue(tsk task.Task) { t.queue = append(t.queue, tsk) }

// Dequeue removes and returns the head task.
func (t *Tenant) Dequeue() task.Task {
	tsk := t.queue[0]
	t.queue = t.queue[1:]
	return tsk
}

// Len returns the number of queued tasks.
func (t *Tenant) Len() int { return len(t.queue) }

// Advance records execution of a task with the given raw cost:
// vt += max(cost, MinCost) / weight (see DESIGN.md §1 and §3).
func (t *Tenant) Advance(cost float64) {
	if cost < MinCost {
		cost = MinCost
	}
	t.vt += cost / t.Weight
}
