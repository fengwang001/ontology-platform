// Package sched 实现加权公平选择：始终取虚拟时间最小的非空租户，
// vt 并列时按租户 ID 字典序打破。选择基于最小堆，O(log n)。
package sched

import (
	"sync"

	"ontology/task"
	"ontology/tenant"
)

// Scheduler 是线程安全的公平队列调度器，状态全部驻留进程内存。
type Scheduler struct {
	mu      sync.Mutex
	tenants map[string]*tenant.Tenant
	inHeap  map[string]bool
	heap    []*tenant.Tenant
	svt     float64
	seq     int64
	cmps    int
}

// New 创建空调度器。
func New() *Scheduler {
	return &Scheduler{tenants: map[string]*tenant.Tenant{}, inHeap: map[string]bool{}}
}

// AddTenant 注册一个已构造的租户。
func (s *Scheduler) AddTenant(t *tenant.Tenant) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tenants[t.ID()] = t
}

// HasTenant 报告租户是否已注册。
func (s *Scheduler) HasTenant(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.tenants[id]
	return ok
}

// Submit 以指定代价入队；返回含全局提交序号的任务。
// 并发下提交顺序以获取本锁的串行化顺序为准（序号锁内分配）。
func (s *Scheduler) Submit(id string, cost float64) (task.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[id]
	if !ok {
		return task.Task{}, ErrNoSuchTenant
	}
	s.seq++
	tk, err := task.New(id, cost, s.seq)
	if err != nil {
		s.seq--
		return task.Task{}, err
	}
	wasEmpty := t.Empty()
	t.Enqueue(tk)
	if wasEmpty {
		t.RaiseVT(s.svt)
		s.push(t)
	}
	return tk, nil
}

// Next 选择并取出下一个应执行的任务；无任务时 ok 为 false。
func (s *Scheduler) Next() (task.Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cmps = 0
	if len(s.heap) == 0 {
		return task.Task{}, false
	}
	t := s.pop()
	s.svt = t.VT()
	tk, _ := t.Dequeue()
	t.Charge(tk.Cost)
	if !t.Empty() {
		s.push(t)
	}
	return tk, true
}

// CmpCount 返回最近一次 Next 中堆比较次数（选择前清零）。
func (s *Scheduler) CmpCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmps
}

// ActiveCount 返回当前非空（在堆中）租户数。
func (s *Scheduler) ActiveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.heap)
}

// TenantCount 返回已注册租户数（含空闲者）。
func (s *Scheduler) TenantCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tenants)
}

// less 是堆比较键：(vt, id) 全序，每次比较计入 cmps。
func (s *Scheduler) less(a, b *tenant.Tenant) bool {
	s.cmps++
	if a.VT() != b.VT() {
		return a.VT() < b.VT()
	}
	return a.ID() < b.ID()
}

func (s *Scheduler) push(t *tenant.Tenant) {
	s.heap = append(s.heap, t)
	s.inHeap[t.ID()] = true
	s.up(len(s.heap) - 1)
}

func (s *Scheduler) pop() *tenant.Tenant {
	n := len(s.heap) - 1
	s.swap(0, n)
	t := s.heap[n]
	s.heap = s.heap[:n]
	if n > 0 {
		s.down(0, n)
	}
	delete(s.inHeap, t.ID())
	return t
}

func (s *Scheduler) up(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !s.less(s.heap[i], s.heap[p]) {
			return
		}
		s.swap(i, p)
		i = p
	}
}

func (s *Scheduler) down(i, n int) {
	for {
		l := 2*i + 1
		if l >= n {
			return
		}
		j := l
		if r := l + 1; r < n && s.less(s.heap[r], s.heap[l]) {
			j = r
		}
		if !s.less(s.heap[j], s.heap[i]) {
			return
		}
		s.swap(i, j)
		i = j
	}
}

func (s *Scheduler) swap(i, j int) {
	s.heap[i], s.heap[j] = s.heap[j], s.heap[i]
}
