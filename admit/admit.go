// Package admit adds per-tenant queue limits (backpressure) on top of
// the scheduler. A full tenant rejects new submissions with
// ErrQueueFull without affecting any other tenant.
package admit

import (
	"errors"
	"fmt"
	"sync"

	"ontology/sched"
	"ontology/task"
)

// ErrQueueFull is returned when a tenant's queue reaches the limit.
var ErrQueueFull = errors.New("admit: tenant queue full")

// Limiter enforces a per-tenant queue cap. Its own mutex makes the
// check-then-submit pair atomic across concurrent Limiter submits.
type Limiter struct {
	mu    sync.Mutex
	s     *sched.Scheduler
	limit int
}

// New wraps s with a per-tenant queue limit.
func New(s *sched.Scheduler, perTenantLimit int) *Limiter {
	return &Limiter{s: s, limit: perTenantLimit}
}

// Submit enqueues tk unless the tenant's queue is already full.
// The failure is per-tenant: other tenants keep submitting normally.
func (l *Limiter) Submit(tk task.Task) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.s.QueueLen(tk.Tenant) >= l.limit {
		return fmt.Errorf("%w: %q at limit %d", ErrQueueFull, tk.Tenant, l.limit)
	}
	return l.s.Submit(tk)
}
