// Package tenant holds a tenant's FIFO queue and virtual-time ledger.
package tenant

import (
	"errors"

	"ontology/task"
)

// ErrInvalidWeight is returned when a tenant is created with weight <= 0.
var ErrInvalidWeight = errors.New("tenant: weight must be positive")

// Tenant is a per-tenant FIFO queue plus its virtual-time ledger.
type Tenant struct {
	ID     string
	Weight float64
	VT     float64 // virtual time: served effective cost divided by weight
	queue  []task.Task
	head   int
}

// New creates a tenant; weight must be positive.
func New(id string, weight float64) (*Tenant, error) {
	if weight <= 0 {
		return nil, ErrInvalidWeight
	}
	return &Tenant{ID: id, Weight: weight}, nil
}

// Enqueue appends a task to the tenant's queue.
func (t *Tenant) Enqueue(tk task.Task) { t.queue = append(t.queue, tk) }

// Dequeue removes and returns the head task; panics if the queue is empty.
func (t *Tenant) Dequeue() task.Task {
	tk := t.queue[t.head]
	t.head++
	if t.head == len(t.queue) {
		t.queue = t.queue[:0]
		t.head = 0
	}
	return tk
}

// Len reports the number of queued tasks.
func (t *Tenant) Len() int { return len(t.queue) - t.head }

// Advance moves the virtual clock forward by eff(cost)/weight.
func (t *Tenant) Advance(effCost int64) { t.VT += float64(effCost) / t.Weight }
