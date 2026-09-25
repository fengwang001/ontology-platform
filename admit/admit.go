// Package admit adds per-tenant queue limits (backpressure) on top of the
// scheduler. All methods are safe for concurrent use.
package admit

import (
	"errors"
	"sync"

	"ontology/sched"
	"ontology/task"
)

// ErrQueueFull is returned when a tenant's queue reaches the configured
// per-tenant limit. Other tenants are unaffected.
var ErrQueueFull = errors.New("admit: tenant queue full")

// Admitter serializes enqueue/dequeue against a scheduler and enforces a
// per-tenant queue limit.
type Admitter struct {
	mu    sync.Mutex
	s     *sched.Scheduler
	limit int
}

// New wraps s with a per-tenant queue limit.
func New(s *sched.Scheduler, perTenantLimit int) *Admitter {
	return &Admitter{s: s, limit: perTenantLimit}
}

// AddTenant registers a tenant; see sched.Scheduler.AddTenant.
func (a *Admitter) AddTenant(id string, weight float64) error {
	return a.s.AddTenant(id, weight)
}

// RemoveTenant removes an idle tenant; see sched.Scheduler.RemoveTenant.
func (a *Admitter) RemoveTenant(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.s.RemoveTenant(id)
}

// Enqueue submits a task if the tenant's queue is below the limit.
func (a *Admitter) Enqueue(t task.Task) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.s.QueueLen(t.Tenant) >= a.limit {
		return ErrQueueFull
	}
	return a.s.Submit(t)
}

// Next removes and returns the next task to execute.
func (a *Admitter) Next() (task.Task, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.s.Next()
}
