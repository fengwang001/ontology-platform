// Package tenant holds a tenant's FIFO queue and virtual-time ledger.
package tenant

import "ontology/task"

// Tenant is one tenant's queue plus its virtual-time ledger.
// It is not safe for concurrent use; the scheduler serializes access.
type Tenant struct {
	ID     string
	Weight float64
	vt     float64
	queue  []task.Task
}

// New creates a tenant with the given ID and weight.
func New(id string, weight float64) *Tenant {
	return &Tenant{ID: id, Weight: weight}
}

// VT returns the tenant's current virtual time.
func (t *Tenant) VT() float64 { return t.vt }

// Len returns the number of queued tasks.
func (t *Tenant) Len() int { return len(t.queue) }

// Push appends a task to the queue.
func (t *Tenant) Push(tk task.Task) { t.queue = append(t.queue, tk) }

// Pop removes and returns the head task. Callers must check Len first.
func (t *Tenant) Pop() task.Task {
	tk := t.queue[0]
	t.queue = t.queue[1:]
	if len(t.queue) == 0 {
		t.queue = nil
	}
	return tk
}

// Advance charges the virtual-time ledger for a served task:
// vt += effectiveCost / Weight (see DESIGN.md section 1).
func (t *Tenant) Advance(effCost float64) { t.vt += effCost / t.Weight }

// Boost raises vt to at least floor, used when an idle tenant
// rejoins so it cannot monopolize the scheduler (DESIGN.md section 2).
func (t *Tenant) Boost(floor float64) {
	if t.vt < floor {
		t.vt = floor
	}
}
