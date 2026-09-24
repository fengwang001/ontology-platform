// Package task defines a unit of work submitted by a tenant.
package task

// Task is a schedulable unit. Cost is measured in abstract executed-work
// units; Seq is the caller-assigned submission sequence number, unique within
// a tenant (it is never interpreted by the scheduler).
type Task struct {
	Tenant string
	Cost   float64
	Seq    uint64
}

// Zero reports whether the task has an empty tenant, i.e. the zero value.
func (t Task) Zero() bool { return t.Tenant == "" }
