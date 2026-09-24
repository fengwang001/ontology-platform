// Package task defines the unit of work submitted by tenants.
package task

// MinCost is the lower bound of the effective cost, so zero-cost tasks
// still advance virtual time and cannot monopolize the scheduler.
const MinCost int64 = 1

// Task is a unit of work submitted by a tenant.
type Task struct {
	Tenant string // owning tenant ID
	Cost   int64  // nominal cost; values below MinCost are clamped
	Seq    uint64 // submission sequence number
}

// Effective returns the cost used to advance virtual time.
func (t Task) Effective() int64 {
	if t.Cost < MinCost {
		return MinCost
	}
	return t.Cost
}
