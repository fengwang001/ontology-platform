// Package sched 用最小堆按虚拟时间 (vt, 租户ID) 选择下一个任务，
// 支持注入时钟、背压判定查询与多协程并发提交/取任务。
package sched

import (
	"container/heap"
	"errors"
	"sync"
	"time"

	"ontology/task"
	"ontology/tenant"
)

var (
	// ErrBadCost 表示任务代价为负。
	ErrBadCost = errors.New("sched: cost must be non-negative")
	// ErrQueueFull 表示该租户队列已达上限。
	ErrQueueFull = errors.New("sched: tenant queue is full")
	// ErrUnknownTenant 表示租户未注册。
	ErrUnknownTenant = errors.New("sched: unknown tenant")
	// ErrTenantActive 表示移除时租户仍有排队任务。
	ErrTenantActive = errors.New("sched: tenant still has queued tasks")
)

// Option 配置调度器。
type Option func(*Scheduler)

// WithClock 注入时钟，任务的 At 字段由它写入。
func WithClock(now func() time.Time) Option {
	return func(s *Scheduler) { s.clock = now }
}

// vtHeap 是非空租户的最小堆，键为 (vt, ID)。
type vtHeap struct {
	q           []*tenant.Queue
	comparisons int64
}

func (h *vtHeap) Len() int { return len(h.q) }

func (h *vtHeap) Less(i, j int) bool {
	h.comparisons++
	a, b := h.q[i], h.q[j]
	if a.VT() != b.VT() {
		return a.VT() < b.VT()
	}
	return a.ID() < b.ID()
}

func (h *vtHeap) Swap(i, j int) { h.q[i], h.q[j] = h.q[j], h.q[i] }

func (h *vtHeap) Push(x any) { h.q = append(h.q, x.(*tenant.Queue)) }

func (h *vtHeap) Pop() any {
	n := len(h.q)
	x := h.q[n-1]
	h.q = h.q[:n-1]
	return x
}

// Scheduler 是并发安全的加权公平队列调度器。
type Scheduler struct {
	mu      sync.Mutex
	cond    *sync.Cond
	tenants map[string]*tenant.Queue
	heap    vtHeap
	sysVT   float64
	seq     int64
	clock   func() time.Time
	closed  bool
	bump    bool
}

// New 创建调度器；默认时钟为 time.Now，默认开启空闲租户 vt 抬升。
func New(opts ...Option) *Scheduler {
	s := &Scheduler{tenants: map[string]*tenant.Queue{}, clock: time.Now, bump: true}
	s.cond = sync.NewCond(&s.mu)
	for _, o := range opts {
		o(s)
	}
	return s
}

// AddTenant 注册一个正权重租户。
func (s *Scheduler) AddTenant(id string, weight float64) error {
	q, err := tenant.NewQueue(id, weight)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tenants[id]; ok {
		return errors.New("sched: tenant already exists")
	}
	s.tenants[id] = q
	return nil
}

// TenantLen 返回某租户当前排队任务数。
func (s *Scheduler) TenantLen(id string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.tenants[id]
	if !ok {
		return 0, ErrUnknownTenant
	}
	return q.Len(), nil
}

// HeapLen 返回堆中非空租户数。
func (s *Scheduler) HeapLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.heap.Len()
}

// Submit 入队一个任务，代价不可为负；返回带全局序号的任务副本。
func (s *Scheduler) Submit(id string, cost float64) (task.Task, error) {
	if cost < 0 {
		return task.Task{}, ErrBadCost
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.tenants[id]
	if !ok {
		return task.Task{}, ErrUnknownTenant
	}
	s.seq++
	t := task.New(id, cost, s.seq)
	emptyBefore := q.Empty()
	if s.bump {
		q.Rejoin(emptyBefore, s.sysVT)
	}
	q.Push(t)
	if emptyBefore {
		heap.Push(&s.heap, q)
	}
	s.cond.Broadcast()
	return t, nil
}

// Next 阻塞取下一个任务；关闭且排空后 ok 为 false。
func (s *Scheduler) Next() (t task.Task, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.heap.Len() == 0 && !s.closed {
		s.cond.Wait()
	}
	if s.heap.Len() == 0 {
		return task.Task{}, false
	}
	s.heap.comparisons = 0
	q := heap.Pop(&s.heap).(*tenant.Queue)
	t, _ = q.Pop()
	t.At = s.clock()
	q.Advance(t.Cost)
	if !q.Empty() {
		heap.Push(&s.heap, q)
	}
	if s.heap.Len() > 0 {
		s.sysVT = s.heap.q[0].VT()
	}
	return t, true
}

// Comparisons 返回最近一次 Next 的堆比较次数。
func (s *Scheduler) Comparisons() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.heap.comparisons
}

// RemoveTenant 移除空租户；仍有排队任务时返回 ErrTenantActive。
func (s *Scheduler) RemoveTenant(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.tenants[id]
	if !ok {
		return ErrUnknownTenant
	}
	if q.Len() > 0 {
		return ErrTenantActive
	}
	delete(s.tenants, id)
	return nil
}

// Close 唤醒所有等待者；排空后 Next 返回 false。
func (s *Scheduler) Close() {
	s.mu.Lock()
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()
}
