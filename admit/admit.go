// Package admit 在调度器之上提供入队背压：每租户队列上限。
package admit

import (
	"errors"
	"sync"

	"ontology/sched"
	"ontology/task"
)

// ErrQueueFull 表示目标租户的等待队列已达上限，入队被拒绝。
var ErrQueueFull = errors.New("admit: tenant queue full")

// Limiter 包装调度器，按租户独立地做入队上限检查。
// 检查与入队在同一互斥锁内完成，并发安全；上限 <= 0 表示不限。
type Limiter struct {
	mu      sync.Mutex
	s       *sched.Scheduler
	def     int
	limits  map[string]int
}

// New 创建包装 s 的限流器，def 为所有租户的默认队列上限（<=0 不限）。
func New(s *sched.Scheduler, def int) *Limiter {
	return &Limiter{s: s, def: def, limits: map[string]int{}}
}

// SetLimit 覆盖单个租户的队列上限（<=0 不限）。
func (l *Limiter) SetLimit(id string, limit int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.limits[id] = limit
}

// Register 注册租户，权重非法时返回 sched.ErrInvalidWeight。
func (l *Limiter) Register(id string, weight float64) error {
	return l.s.AddTenant(id, weight)
}

// Submit 检查上限后将任务入队。未注册租户返回 sched.ErrUnknownTenant，
// 超限返回 ErrQueueFull；检查按租户独立，不影响其他租户。
func (l *Limiter) Submit(id string, tk task.Task) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	qlen, ok := l.s.QLen(id)
	if !ok {
		return sched.ErrUnknownTenant
	}
	limit := l.def
	if v, ok := l.limits[id]; ok {
		limit = v
	}
	if limit > 0 && qlen >= limit {
		return ErrQueueFull
	}
	return l.s.Submit(id, tk)
}
