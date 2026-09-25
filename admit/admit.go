// Package admit provides admission control and backpressure on top of
// the scheduler: per-tenant queue limits, weight validation, and
// tenant removal rules.
package admit

import (
	"ontology/sched"
	"ontology/task"
)

// Admission errors, re-exported from sched so a single errors.Is check
// works no matter which package the caller holds.
var (
	ErrQueueFull     = sched.ErrQueueFull
	ErrInvalidWeight = sched.ErrInvalidWeight
	ErrTenantBusy    = sched.ErrTenantBusy
	ErrUnknownTenant = sched.ErrUnknownTenant
)

// Controller wraps a scheduler with a per-tenant queue limit.
type Controller struct {
	sch   *sched.Scheduler
	limit int
}

// New creates a controller enforcing perTenantLimit queued tasks per
// tenant. A full tenant never blocks submissions of other tenants.
func New(s *sched.Scheduler, perTenantLimit int) *Controller {
	return &Controller{sch: s, limit: perTenantLimit}
}

// Register adds a tenant. A zero or negative weight is rejected with
// ErrInvalidWeight.
func (c *Controller) Register(id string, weight float64) error {
	return c.sch.Register(id, weight)
}

// Submit enqueues a task. It fails with ErrUnknownTenant for
// unregistered tenants and with ErrQueueFull when the tenant's queue
// reached the configured limit; other tenants are unaffected.
func (c *Controller) Submit(tk task.Task) error {
	return c.sch.Enqueue(tk, c.limit)
}

// Remove deletes a tenant. It refuses with ErrTenantBusy while tasks
// are still queued, so queued work is never silently dropped; drain
// the queue (via Next) before removing.
func (c *Controller) Remove(id string) error {
	return c.sch.Remove(id)
}

// Next returns the next task to execute across all tenants.
func (c *Controller) Next() (task.Task, bool) {
	return c.sch.Next()
}
