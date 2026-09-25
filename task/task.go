// Package task defines the unit of work submitted by tenants.
package task

// Task is a single unit of work. Cost is the execution cost charged to the
// tenant's virtual-time ledger; zero is legal (clamped to a minimum at
// scheduling time). Seq is a globally unique submission sequence number
// assigned by the scheduler under its enqueue lock.
type Task struct {
	Tenant string
	Cost   float64
	Seq    uint64
}
