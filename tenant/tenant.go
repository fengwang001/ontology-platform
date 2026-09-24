// Package tenant holds a per-tenant FIFO queue and its virtual-time ledger.
package tenant

import (
	"errors"
	"math"

	"ontology/task"
)

// ErrBadWeight is returned for non-positive, non-finite or out-of-range weights.
var ErrBadWeight = errors.New("tenant: invalid weight (must be finite in [1e-9,1e9])")

const (
	// MinWeight and MaxWeight bound the practical safe weight range.
	MinWeight = 1e-9
	MaxWeight = 1e9
	// MinVCost floors virtual-time progress so zero-cost tasks cannot hog.
	MinVCost = 1e-2
)

// Tenant is one weighted FIFO queue with a virtual-time ledger.
type Tenant struct {
	ID     string
	weight float64
	vt     float64
	q      []task.Task
	index  int // heap index, maintained by sched; -1 when not in heap
}

// New validates weight and constructs an empty tenant.
func New(id string, weight float64) (*Tenant, error) {
	if weight <= 0 || math.IsNaN(weight) || math.IsInf(weight, 0) ||
		weight < MinWeight || weight > MaxWeight {
		return nil, ErrBadWeight
	}
	return &Tenant{ID: id, weight: weight, index: -1}, nil
}

// Weight returns the configured weight.
func (t *Tenant) Weight() float64 { return t.weight }

// VT returns the current virtual time.
func (t *Tenant) VT() float64 { return t.vt }

// Len returns the queued task count.
func (t *Tenant) Len() int { return len(t.q) }

// Index returns the heap index (-1 when absent).
func (t *Tenant) Index() int { return t.index }

// SetIndex records the heap index.
func (t *Tenant) SetIndex(i int) { t.index = i }

// WasEmpty reports whether the queue is empty before a push (for rejoin lifting).
func (t *Tenant) WasEmpty() bool { return len(t.q) == 0 }

// Rejoin lifts vt to the system virtual time when an idle tenant becomes busy.
func (t *Tenant) Rejoin(systemVT float64) {
	if systemVT > t.vt {
		t.vt = systemVT
	}
}

// Push appends a task to the FIFO.
func (t *Tenant) Push(j task.Task) { t.q = append(t.q, j) }

// Pop removes and returns the head task.
func (t *Tenant) Pop() task.Task {
	j := t.q[0]
	t.q = t.q[1:]
	return j
}

// Drain removes and returns all queued tasks (used on tenant removal).
func (t *Tenant) Drain() []task.Task {
	out := t.q
	t.q = nil
	return out
}

// Advance applies virtual-time progress for serving a task of the given cost.
// Zero/near-zero cost still advances by MinVCost so the tenant cannot hog.
func (t *Tenant) Advance(cost float64) {
	d := cost / t.weight
	if d < MinVCost {
		d = MinVCost
	}
	t.vt += d
}
