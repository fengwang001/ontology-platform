// Package task defines the unit of work submitted by tenants.
package task

// Task is a unit of work submitted by a tenant.
type Task struct {
	Tenant string  // owning tenant ID
	Cost   float64 // execution cost; zero is clamped to a minimum by the scheduler
	Seq    uint64  // submission sequence number, unique per run
}
