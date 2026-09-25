// Package tenant holds a tenant's FIFO queue and virtual-time ledger.
package tenant

import (
	"errors"
	"math"

	"ontology/task"
)

// ErrInvalidWeight is returned when a tenant weight is not a finite
// positive number.
var ErrInvalidWeight = errors.New("tenant: weight must be a finite positive number")

// Tenant is a tenant's FIFO queue plus its virtual-time ledger.
type Tenant struct {
	id     string
	weight float64
	vt     float64
	queue  []task.Task
}

// New creates a tenant with the given ID and weight.
// Weight must be a finite positive number, otherwise ErrInvalidWeight.
func New(id string, weight float64) (*Tenant, error) {
	if weight <= 0 || math.IsNaN(weight) || math.IsInf(weight, 0) {
		return nil, ErrInvalidWeight
	}
	return &Tenant{id: id, weight: weight}, nil
}

// ID returns the tenant ID.
func (t *Tenant) ID() string { return t.id }

// Weight returns the tenant weight.
func (t *Tenant) Weight() float64 { return t.weight }

// VT returns the current virtual time.
func (t *Tenant) VT() float64 { return t.vt }

// LiftTo raises the virtual time to v if v is larger. Used when an idle
// tenant becomes non-empty again so it cannot monopolize the scheduler.
func (t *Tenant) LiftTo(v float64) {
	if v > t.vt {
		t.vt = v
	}
}

// Advance charges cost against the ledger: vt += cost / weight.
func (t *Tenant) Advance(cost float64) { t.vt += cost / t.weight }

// Push appends a task to the tenant's queue.
func (t *Tenant) Push(tsk task.Task) { t.queue = append(t.queue, tsk) }

// Pop removes and returns the head task. Caller must ensure Len() > 0.
func (t *Tenant) Pop() task.Task {
	tsk := t.queue[0]
	t.queue = t.queue[1:]
	return tsk
}

// Len returns the number of queued tasks.
func (t *Tenant) Len() int { return len(t.queue) }
