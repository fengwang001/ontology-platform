// Package tenant holds one tenant's FIFO queue and virtual-time ledger.
package tenant

import "ontology/task"

// MinCost is the smallest effective cost charged to virtual time, so that a
// zero-cost task still advances virtual time and cannot monopolise the queue.
const MinCost = 1e-9

// Tenant is a FIFO queue plus the virtual-time key used by the scheduler heap.
type Tenant struct {
	ID     string
	Weight float64
	VT     float64
	q      []task.Task
}

// New creates a tenant with positive weight and virtual time vt.
func New(id string, weight, vt float64) *Tenant {
	return &Tenant{ID: id, Weight: weight, VT: vt}
}

// Len is the number of queued tasks.
func (t *Tenant) Len() int { return len(t.q) }

// Push appends a task to the FIFO.
func (t *Tenant) Push(tk task.Task) { t.q = append(t.q, tk) }

// Pop removes and returns the head task; it panics on an empty queue.
func (t *Tenant) Pop() task.Task {
	tk := t.q[0]
	copy(t.q, t.q[1:])
	t.q[len(t.q)-1] = task.Task{}
	t.q = t.q[:len(t.q)-1]
	return tk
}

// Charge advances virtual time for executing a task of the given cost. Costs
// below MinCost are clamped so virtual time always moves forward.
func (t *Tenant) Charge(cost float64) {
	if cost < MinCost {
		cost = MinCost
	}
	t.VT += cost / t.Weight
}

// CatchUp raises virtual time to at least sysVT when a tenant rejoins after
// being empty, so idle debt is neither rewarded nor punished.
func (t *Tenant) CatchUp(sysVT float64) {
	if sysVT > t.VT {
		t.VT = sysVT
	}
}
