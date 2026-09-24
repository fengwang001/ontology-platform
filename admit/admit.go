// Package admit provides admission control (per-tenant queue caps), the
// rejoin virtual-time lift policy, and concurrency safety on top of sched.
package admit

import (
	"errors"
	"sync"

	"ontology/sched"
	"ontology/task"
	"ontology/tenant"
)

var (
	// ErrQueueFull reports that a tenant's queue reached its cap.
	ErrQueueFull = errors.New("admit: tenant queue full")
	// ErrTenantBusy reports removal of a tenant that still has queued tasks.
	ErrTenantBusy = errors.New("admit: tenant has queued tasks")
	// ErrUnknownTenant reports an operation on an unregistered tenant.
	ErrUnknownTenant = errors.New("admit: unknown tenant")
)

// Limiter admits tasks into per-tenant queues and hands them out in
// weighted-fair order. It is safe for concurrent use; the submission
// order under concurrency is the lock acquisition order.
type Limiter struct {
	mu      sync.Mutex
	sched   *sched.Scheduler
	tenants map[string]*tenant.Tenant
	queued  map[*tenant.Tenant]bool
	cap     int
}

// New creates a Limiter with the given per-tenant queue cap.
func New(queueCap int) *Limiter {
	return &Limiter{
		sched:   sched.New(),
		tenants: make(map[string]*tenant.Tenant),
		queued:  make(map[*tenant.Tenant]bool),
		cap:     queueCap,
	}
}

// Register adds a tenant; weight must be positive (tenant.ErrInvalidWeight).
func (l *Limiter) Register(id string, weight float64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.tenants[id]; ok {
		return nil
	}
	t, err := tenant.New(id, weight)
	if err != nil {
		return err
	}
	l.tenants[id] = t
	return nil
}

// Submit enqueues a task. When the tenant's queue was empty its virtual
// time is lifted to max(VT, sysVT) so it cannot monopolize the scheduler.
func (l *Limiter) Submit(tk task.Task) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	t, ok := l.tenants[tk.Tenant]
	if !ok {
		return ErrUnknownTenant
	}
	if t.Len() >= l.cap {
		return ErrQueueFull
	}
	if t.Len() == 0 && l.sched.SysVT() > t.VT {
		t.VT = l.sched.SysVT()
	}
	t.Enqueue(tk)
	if !l.queued[t] {
		l.sched.Add(t)
		l.queued[t] = true
	}
	return nil
}

// Next removes and returns the next task in weighted-fair order.
func (l *Limiter) Next() (task.Task, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.sched.Pop()
	if t == nil {
		return task.Task{}, false
	}
	tk := t.Dequeue()
	t.Advance(tk.Effective())
	if t.Len() > 0 {
		l.sched.Add(t)
	} else {
		l.queued[t] = false
	}
	return tk, true
}

// Remove unregisters a tenant. It is refused while tasks remain queued;
// queued tasks stay in place and remain schedulable.
func (l *Limiter) Remove(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	t, ok := l.tenants[id]
	if !ok {
		return ErrUnknownTenant
	}
	if t.Len() > 0 {
		return ErrTenantBusy
	}
	delete(l.tenants, id)
	return nil
}

// Scheduler exposes the underlying scheduler for read-only inspection.
// Callers must not use it concurrently with Limiter operations.
func (l *Limiter) Scheduler() *sched.Scheduler { return l.sched }
