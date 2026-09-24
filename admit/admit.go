// Package admit 在 sched 之上提供入队背压、租户管理与并发安全。
package admit

import (
	"errors"
	"math"
	"sync"

	"ontology/sched"
	"ontology/task"
	"ontology/tenant"
)

// 四类可判定错误，用 errors.Is 区分。
var (
	ErrInvalidWeight = errors.New("admit: weight must be a positive finite number")
	ErrQueueFull     = errors.New("admit: tenant queue full")
	ErrTenantBusy    = errors.New("admit: tenant has queued tasks")
	ErrUnknownTenant = errors.New("admit: unknown tenant")
)

// Limiter 包装 Scheduler：每租户队列上限 + 互斥锁串行化，并发安全。
type Limiter struct {
	mu  sync.Mutex
	sch *sched.Scheduler
	cap int
}

// New 创建 Limiter；cap 为每租户队列上限（<=0 表示不限）。
func New(sch *sched.Scheduler, cap int) *Limiter {
	return &Limiter{sch: sch, cap: cap}
}

// AddTenant 注册租户；权重 <=0、NaN、Inf 返回 ErrInvalidWeight。
func (l *Limiter) AddTenant(id string, weight float64) error {
	if weight <= 0 || math.IsNaN(weight) || math.IsInf(weight, 0) {
		return ErrInvalidWeight
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sch.Add(tenant.New(id, weight))
	return nil
}

// RemoveTenant 移除租户；有排队任务时拒绝（ErrTenantBusy），不静默丢任务。
func (l *Limiter) RemoveTenant(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sch.Tenant(id) == nil {
		return ErrUnknownTenant
	}
	if !l.sch.Remove(id) {
		return ErrTenantBusy
	}
	return nil
}

// Submit 入队；超每租户上限返回 ErrQueueFull，不影响其他租户。
func (l *Limiter) Submit(tk task.Task) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.sch.Tenant(tk.Tenant)
	if t == nil {
		return ErrUnknownTenant
	}
	if l.cap > 0 && t.Len() >= l.cap {
		return ErrQueueFull
	}
	l.sch.Submit(tk)
	return nil
}

// Next 取出下一个任务；锁保证每个任务恰好被取出一次。
func (l *Limiter) Next() (task.Task, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sch.Next()
}

// QueueLen 返回某租户当前排队数（未注册返回 -1）。
func (l *Limiter) QueueLen(id string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if t := l.sch.Tenant(id); t != nil {
		return t.Len()
	}
	return -1
}
