// Package task defines the unit of work handled by the scheduler.
package task

// Task is a unit of work submitted by a tenant.
type Task struct {
	Tenant string // owning tenant ID
	Cost   int64  // service cost charged to the tenant's virtual time
	Seq    uint64 // submission sequence number
}
