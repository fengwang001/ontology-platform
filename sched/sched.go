// Package sched 用最小堆按虚拟时间实现加权公平选择，并发安全。
package sched

import (
	"sync"

	"ontology/task"
	"ontology/tenant"
)

const eps = 1e-12

// Scheduler 维护租户表与非空租户最小堆。
type Scheduler struct {
	mu      sync.Mutex
	tenants map[string]*tenant.Tenant
	heap    []*tenant.Tenant
	cmp     int // 单次选择的比较计数
}

// New 创建空调度器。
func New() *Scheduler {
	return &Scheduler{tenants: make(map[string]*tenant.Tenant)}
}

// AddTenant 注册租户；已存在则视为成功。权重非法返回 task.ErrInvalidWeight。
func (s *Scheduler) AddTenant(id string, weight float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tenants[id]; ok {
		return nil
	}
	t, err := tenant.New(id, weight)
	if err != nil {
		return err
	}
	s.tenants[id] = t
	return nil
}

// RemoveTenant 移除租户；队列非空时返回 task.ErrTenantBusy。
func (s *Scheduler) RemoveTenant(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[id]
	if !ok {
		return nil
	}
	if !t.Empty() {
		return task.ErrTenantBusy
	}
	delete(s.tenants, id)
	return nil
}

// Submit 入队任务。租户从空转非空时把 vt 抬到当前系统虚拟时间。
func (s *Scheduler) Submit(j task.Task) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tenants[j.Tenant]
	if t == nil { // 未显式注册时以权重 1 加入。
		t, _ = tenant.New(j.Tenant, 1)
		s.tenants[j.Tenant] = t
	}
	if t.Empty() {
		t.Bump(s.nowLocked())
		s.pushHeap(t)
	}
	t.Push(j)
}

// Next 取出并返回下一个应执行任务；无任务时 ok 为 false。
func (s *Scheduler) Next() (task.Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cmp = 0
	if len(s.heap) == 0 {
		return task.Task{}, false
	}
	t := s.popHeap()
	j, _ := t.Pop()
	t.Advance(j.Cost)
	if !t.Empty() {
		s.pushHeap(t)
	}
	return j, true
}

// Active 返回当前堆中非空租户数量。
func (s *Scheduler) Active() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.heap)
}

// Len 返回已注册租户总数。
func (s *Scheduler) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tenants)
}

// LastCmp 返回上一次 Next 的比较次数。
func (s *Scheduler) LastCmp() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmp
}

// nowLocked 返回当前系统虚拟时间：堆顶（最小 vt）之外的最大 vt；
// 空闲租户应抬到“大家所在水平”，取堆中最大 vt，空堆取 0。
func (s *Scheduler) nowLocked() float64 {
	var now float64
	for _, t := range s.heap {
		if t.VT() > now {
			now = t.VT()
		}
	}
	return now
}

func (s *Scheduler) less(i, j int) bool {
	a, b := s.heap[i], s.heap[j]
	if d := a.VT() - b.VT(); d > eps {
		return false
	} else if d < -eps {
		return true
	}
	return a.ID() < b.ID()
}

func (s *Scheduler) pushHeap(t *tenant.Tenant) {
	s.heap = append(s.heap, t)
	s.up(len(s.heap) - 1)
}

func (s *Scheduler) popHeap() *tenant.Tenant {
	n := len(s.heap) - 1
	s.swap(0, n)
	s.down(0, n)
	t := s.heap[n]
	s.heap = s.heap[:n]
	return t
}

func (s *Scheduler) swap(i, j int) { s.heap[i], s.heap[j] = s.heap[j], s.heap[i] }

func (s *Scheduler) up(j int) {
	for j > 0 {
		parent := (j - 1) / 2
		s.cmp++
		if s.less(j, parent) {
			s.swap(j, parent)
			j = parent
			continue
		}
		return
	}
}

func (s *Scheduler) down(i, n int) {
	for {
		left := 2*i + 1
		if left >= n {
			return
		}
		smallest := left
		if right := left + 1; right < n {
			s.cmp++
			if s.less(right, left) {
				smallest = right
			}
		}
		s.cmp++
		if !s.less(smallest, i) {
			return
		}
		s.swap(i, smallest)
		i = smallest
	}
}
