// Package sched 实现加权公平选择：虚拟时间最小者优先，
// 同虚拟时间按租户 ID 字典序打破并列。
package sched

import (
	"errors"
	"sync"

	"ontology/task"
	"ontology/tenant"
)

// MinCost 为推进虚拟时间时的最小计费代价下界，
// 防止零代价任务不推进 vt 而无限独占调度器（见 DESIGN.md 第 3 节）。
const MinCost = 1.0

// 哨兵错误，可用 errors.Is 判定。
var (
	// ErrInvalidWeight 表示注册的权重为零或负。
	ErrInvalidWeight = errors.New("sched: weight must be positive")
	// ErrUnknownTenant 表示目标租户未注册。
	ErrUnknownTenant = errors.New("sched: unknown tenant")
	// ErrTenantBusy 表示移除时租户仍有排队任务，拒绝移除。
	ErrTenantBusy = errors.New("sched: tenant has queued tasks")
)

// Scheduler 是多租户加权公平调度器。全部状态由一把互斥锁保护，
// 提交与取出在锁内串行化，并发安全。
type Scheduler struct {
	mu      sync.Mutex
	tenants map[string]*tenant.Tenant
	heap    []*tenant.Tenant // 仅含非空租户，按 (VT, ID) 最小堆
	sysVT   float64          // 系统虚拟时间：被选中者执行前 vt 的单调上界
	cmp     int              // 非导出计数器：最近一次 Pick 的堆比较次数
}

// New 创建空调度器。
func New() *Scheduler {
	return &Scheduler{tenants: make(map[string]*tenant.Tenant)}
}

// AddTenant 注册租户。权重必须为正，否则返回 ErrInvalidWeight。
// 重复注册同一 ID 为幂等空操作。
func (s *Scheduler) AddTenant(id string, weight float64) error {
	if weight <= 0 {
		return ErrInvalidWeight
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tenants[id]; !ok {
		s.tenants[id] = tenant.New(id, weight)
	}
	return nil
}

// RemoveTenant 移除租户。仍有排队任务时返回 ErrTenantBusy 并保留任务；
// 未注册返回 ErrUnknownTenant。
func (s *Scheduler) RemoveTenant(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[id]
	if !ok {
		return ErrUnknownTenant
	}
	if !t.Empty() {
		return ErrTenantBusy
	}
	delete(s.tenants, id)
	return nil
}

// Submit 将任务加入对应租户队列。租户未注册返回 ErrUnknownTenant。
// 租户从空转为非空时，vt 抬升到 max(vt, sysVT) 后再入堆。
func (s *Scheduler) Submit(id string, tk task.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[id]
	if !ok {
		return ErrUnknownTenant
	}
	tk.Tenant = id
	wasEmpty := t.Empty()
	t.Push(tk)
	if wasEmpty {
		t.Lift(s.sysVT)
		s.push(t)
	}
	return nil
}

// Pick 选出下一个应执行的任务：取堆顶租户（vt 最小，ID 字典序打破并列），
// 弹出队首任务，按 max(cost, MinCost)/weight 推进其 vt；
// 队列仍非空则下滤回堆，否则移出堆。无任务时返回 ok=false。
func (s *Scheduler) Pick() (task.Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cmp = 0
	if len(s.heap) == 0 {
		return task.Task{}, false
	}
	t := s.heap[0]
	tk := t.Pop()
	if t.VT > s.sysVT {
		s.sysVT = t.VT
	}
	cost := tk.Cost
	if cost < MinCost {
		cost = MinCost
	}
	t.Advance(cost)
	if t.Empty() {
		s.popRoot()
	} else {
		s.down(0)
	}
	return tk, true
}

// LastComparisons 返回最近一次 Pick 中堆比较的次数（用于复杂度断言）。
func (s *Scheduler) LastComparisons() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmp
}

// HeapLen 返回堆中租户数，等于当前非空租户数。
func (s *Scheduler) HeapLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.heap)
}

// QLen 返回租户等待队列长度；ok=false 表示租户未注册。
func (s *Scheduler) QLen(id string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[id]
	if !ok {
		return 0, false
	}
	return t.Len(), true
}

// less 为堆比较键 (VT, ID)，同时累计比较次数。
func (s *Scheduler) less(i, j int) bool {
	s.cmp++
	a, b := s.heap[i], s.heap[j]
	if a.VT != b.VT {
		return a.VT < b.VT
	}
	return a.ID < b.ID
}

func (s *Scheduler) swap(i, j int) {
	s.heap[i], s.heap[j] = s.heap[j], s.heap[i]
}

func (s *Scheduler) push(t *tenant.Tenant) {
	s.heap = append(s.heap, t)
	s.up(len(s.heap) - 1)
}

func (s *Scheduler) popRoot() {
	n := len(s.heap) - 1
	s.heap[0] = s.heap[n]
	s.heap = s.heap[:n]
	if n > 0 {
		s.down(0)
	}
}

func (s *Scheduler) up(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !s.less(i, p) {
			return
		}
		s.swap(i, p)
		i = p
	}
}

func (s *Scheduler) down(i int) {
	n := len(s.heap)
	for {
		l := 2*i + 1
		if l >= n {
			return
		}
		m := l
		if r := l + 1; r < n && s.less(r, l) {
			m = r
		}
		if !s.less(m, i) {
			return
		}
		s.swap(m, i)
		i = m
	}
}
