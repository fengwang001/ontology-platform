// Package tenant holds a tenant's FIFO queue and virtual-time ledger.
package tenant

import (
	"errors"
	"fmt"
	"math"

	"ontology/task"
)

// MinCost is the lower bound applied to a task's effective cost so that even
// zero-cost tasks advance the tenant's virtual time and cannot monopolize
// the scheduler forever.
const MinCost = 1.0

// Weight bounds keep virtual-time arithmetic finite in float64 (see DESIGN.md).
const (
	MinWeight = 1e-6
	MaxWeight = 1e6
)

// ErrInvalidWeight is returned when a tenant weight is non-positive, NaN, or
// outside [MinWeight, MaxWeight].
var ErrInvalidWeight = errors.New("tenant: invalid weight")

// Tenant is a weighted FIFO queue plus its virtual-time ledger. It is not
// safe for concurrent use; callers (sched) serialize access.
type Tenant struct {
	ID     string
	Weight float64

	vt     float64
	queue  []task.Task
	active bool // currently present in the scheduler heap
}

// New validates the weight and returns a ready tenant.
func New(id string, weight float64) (*Tenant, error) {
	if math.IsNaN(weight) || weight < MinWeight || weight > MaxWeight {
		return nil, fmt.Errorf("%w: %g", ErrInvalidWeight, weight)
	}
	return &Tenant{ID: id, Weight: weight}, nil
}

// VT returns the tenant's current virtual time.
func (t *Tenant) VT() float64 { return t.vt }

// Len returns the number of queued tasks.
func (t *Tenant) Len() int { return len(t.queue) }

// Active reports whether the tenant is currently in the scheduler heap.
func (t *Tenant) Active() bool { return t.active }

// SetActive marks heap membership; only the scheduler may call it.
func (t *Tenant) SetActive(active bool) { t.active = active }

// Enqueue appends a task to the tenant's queue.
func (t *Tenant) Enqueue(tsk task.Task) { t.queue = append(t.queue, tsk) }

// Dequeue removes and returns the head task. Callers must ensure Len() > 0.
func (t *Tenant) Dequeue() task.Task {
	tsk := t.queue[0]
	t.queue = t.queue[1:]
	return tsk
}

// Lift raises the tenant's virtual time to floor if it has fallen behind,
// discarding stale credit accumulated while idle (see DESIGN.md section 2).
func (t *Tenant) Lift(floor float64) {
	if t.vt < floor {
		t.vt = floor
	}
}

// Advance charges a completed task to the ledger: vt += max(cost, MinCost)/w.
func (t *Tenant) Advance(cost float64) {
	if cost < MinCost {
		cost = MinCost
	}
	t.vt += cost / t.Weight
}
