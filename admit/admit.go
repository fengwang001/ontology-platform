// Package admit adds per-tenant queue limits (backpressure) on top of sched.
package admit

import (
	"errors"
	"fmt"
	"sync"

	"ontology/sched"
	"ontology/task"
)

// ErrQueueFull is returned when a tenant's queue reaches the configured
// limit. It only affects that tenant; others may still enqueue.
var ErrQueueFull = errors.New("admit: tenant queue full")

// Limiter wraps a Scheduler with a per-tenant queue depth limit. The
// check-and-enqueue pair is serialized by l.mu so the limit holds under
// concurrent submitters. Lock order is always Limiter -> Scheduler.
type Limiter struct {
	s     *sched.Scheduler
	limit int
	mu    sync.Mutex
	depth map[string]int
}

// New wraps s with a per-tenant queue limit.
func New(s *sched.Scheduler, limit int) *Limiter {
	return &Limiter{s: s, limit: limit, depth: make(map[string]int)}
}

// Submit enqueues a task if the tenant is below its queue limit.
func (l *Limiter) Submit(t task.Task) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.depth[t.Tenant] >= l.limit {
		return fmt.Errorf("%w: %s", ErrQueueFull, t.Tenant)
	}
	if err := l.s.Submit(t); err != nil {
		return err
	}
	l.depth[t.Tenant]++
	return nil
}

// Next takes the next scheduled task and releases one queue slot.
func (l *Limiter) Next() (task.Task, bool) {
	t, ok := l.s.Next()
	if ok {
		l.mu.Lock()
		l.depth[t.Tenant]--
		l.mu.Unlock()
	}
	return t, ok
}
