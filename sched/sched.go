// Package sched 用最小堆按 (虚拟时间, 租户ID) 做加权公平选择，并发安全。
package sched

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/task"
	"ontology/tenant"
)

// 四类错误：admit.Queue 复用 ErrQueueFull，其余由本包直接返回。
var (
	ErrQueueFull     = errors.New("sched: per-tenant queue full")
	ErrInvalidWeight = tenant.ErrInvalidWeight
	ErrInvalidCost   = tenant.ErrInvalidCost
	ErrTenantGone    = tenant.ErrTenantGone
)

// Option 配置调度器。
type Option func(*Scheduler)

// WithMinCost 设置零代价任务的虚拟时间推进下界 eps（默认 1e-9）。
func WithMinCost(eps float64) Option { return func(s *Scheduler) { s.eps = eps } }

// WithRaiseOff 关闭空闲租户重新加入时的 vt 抬升（仅用于对照测试）。
func WithRaiseOff() Option { return func(s *Scheduler) { s.raise = false } }

// Scheduler 是多租户公平队列调度器。
type Scheduler struct {
	mu    sync.Mutex
	cond  *sync.Cond
	tenants map[string]*tenant.Tenant
	hp      vtHeap
	seq     uint64
	eps     float64
	raise bool
	closed bool

	// cmpCount 记录"最近一次选择"在堆维护中的比较次数；hpSize 为当前堆大小。
	cmpCount int
	hpSize   int
}

// New 创建调度器。
func New(opts ...Option) *Scheduler {
	s := &Scheduler{tenants: map[string]*tenant.Tenant{}, eps: 1e-9, raise: true}
	for _, o := range opts {
		o(s)
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// AddTenant 注册租户，权重必须 >0；重复 ID 报错。
func (s *Scheduler) AddTenant(id string, weight float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tenants[id]; ok {
		return errors.New("sched: duplicate tenant " + id)
	}
	tn, err := tenant.New(id, weight)
	if err != nil {
		return err
	}
	s.tenants[id] = tn
	return nil
}

// Submit 入队一个任务，返回带全局序号的任务副本。空转非空时抬升 vt 并入堆。
func (s *Scheduler) Submit(id string, cost float64) (task.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tn, ok := s.tenants[id]
	if !ok {
		return task.Task{}, ErrTenantGone
	}
	if cost < 0 {
		return task.Task{}, ErrInvalidCost
	}
	s.seq++
	t := task.New(id, cost, s.seq)
	wasEmpty := tn.WasEmpty()
	if err := tn.Enqueue(t); err != nil {
		return task.Task{}, err
	}
	if wasEmpty {
		if s.raise {
			tn.RaiseVT(s.hp.TopVT())
		}
		tn.SetInHeap(true)
		heap.Push(&s.hp, tn)
		s.hpSize = s.hp.Len()
	}
	s.cond.Broadcast()
	return t, nil
}

// Pop 阻塞直到可取出下一个公平任务；Close 后返回零值与 false。
func (s *Scheduler) Pop() (task.Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.hp.Len() == 0 && !s.closed {
		s.cond.Wait()
	}
	if s.hp.Len() == 0 {
		return task.Task{}, false
	}
	return s.popLocked(), true
}

// TryPop 非阻塞版本。
func (s *Scheduler) TryPop() (task.Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hp.Len() == 0 {
		return task.Task{}, false
	}
	return s.popLocked(), true
}

func (s *Scheduler) popLocked() task.Task {
	s.hp.beginCmp()
	tn := heap.Pop(&s.hp).(*tenant.Tenant)
	t, _ := tn.Dequeue()
	eff := t.Cost()
	if eff < s.eps {
		eff = s.eps
	}
	tn.Advance(eff)
	if tn.Len() > 0 { // 队列仍非空，重新入堆参与下一轮
		heap.Push(&s.hp, tn)
	} else {
		tn.SetInHeap(false)
	}
	s.cmpCount = s.hp.endCmp()
	s.hpSize = s.hp.Len()
	return t
}

// LastCmp 返回最近一次选择引发的堆比较次数；HeapSize 返回当前堆元素数。
func (s *Scheduler) LastCmp() int { s.mu.Lock(); defer s.mu.Unlock(); return s.cmpCount }
func (s *Scheduler) HeapSize() int { s.mu.Lock(); defer s.mu.Unlock(); return s.hpSize }

// RemoveTenant 仅允许在该租户队列排空后移除；有排队任务返回 ErrTenantGone。
func (s *Scheduler) RemoveTenant(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tn, ok := s.tenants[id]
	if !ok {
		return ErrTenantGone
	}
	if tn.Len() > 0 {
		return ErrTenantGone
	}
	if tn.InHeap() {
		for i, e := range s.hp {
			if e == tn {
				heap.Remove(&s.hp, i)
				break
			}
		}
	}
	tn.MarkGone()
	delete(s.tenants, id)
	s.hpSize = s.hp.Len()
	return nil
}

// Close 唤醒所有阻塞消费者。
func (s *Scheduler) Close() {
	s.mu.Lock()
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()
}
