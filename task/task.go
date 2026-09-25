// Package task defines the unit of work submitted by tenants.
package task

// Task is a unit of work submitted by a tenant.
type Task struct {
	// Tenant is the ID of the owning tenant.
	Tenant string
	// Cost is the execution cost charged against the tenant's share.
	// Zero is legal; the scheduler clamps it to a positive lower bound.
	Cost float64
	// Seq is the submission sequence number, assigned by the submitter.
	Seq uint64
}
