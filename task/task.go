// Package task defines the unit of work submitted by tenants.
package task

// Task is a single unit of work in the scheduler.
type Task struct {
	// Tenant is the ID of the owning tenant.
	Tenant string
	// Cost is the execution cost charged to the tenant's virtual time.
	// Zero is legal; the tenant package bills at least MinCost for it.
	Cost float64
	// Seq is the submission sequence number, unique per task.
	Seq uint64
}
