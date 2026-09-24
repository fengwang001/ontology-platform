// Package task defines the unit of work submitted by tenants.
package task

// Task is a single unit of work submitted by a tenant.
type Task struct {
	// Tenant is the ID of the submitting tenant.
	Tenant string
	// Cost is the nominal execution cost; 0 is legal and is
	// clamped to a minimum for virtual-time accounting.
	Cost float64
	// Seq is the per-tenant submission sequence number, assigned
	// by the caller; it makes deterministic streams comparable.
	Seq int64
}

// MinCost is the lower bound applied to Cost for virtual-time
// accounting, so zero-cost tasks still advance a tenant's vt.
const MinCost = 1.0

// Effective returns the cost used for virtual-time accounting.
func (t Task) Effective() float64 {
	if t.Cost < MinCost {
		return MinCost
	}
	return t.Cost
}
