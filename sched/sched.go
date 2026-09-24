// Package sched 实现加权公平选择：虚拟时间最小者优先，堆维护非空租户。
package sched

import (
	"container/heap"

	"ontology/task"
	"ontology/tenant"
)

// MinCost 是 vt 记账的代价下界：零/负代价任务也推进虚拟时间，防止独占。
const MinCost = 1.0

// vtHeap 是最小二叉堆，按 (vt, ID 字典序) 排序；cmps 统计比较次数。
type vtHeap struct {
	items []*tenant.Tenant
	cmps  *int
}

func (h *vtHeap) Len() int { return len(h.items) }

func (h *vtHeap) Less(i, j int) bool {
	*h.cmps++
	a, b := h.items[i], h.items[j]
	if a.VT() != b.VT() {
		return a.VT() < b.VT()
	}
	return a.ID < b.ID
}

func (h *vtHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *vtHeap) Push(x any) { h.items = append(h.items, x.(*tenant.Tenant)) }

func (h *vtHeap) Pop() any {
	old := h.items
	n := len(old)
	it := old[n-1]
	h.items = old[:n-1]
	return it
}

// Scheduler 在注册租户间做加权公平选择。非并发安全，由 admit 层加锁。
type Scheduler struct {
	tenants map[string]*tenant.Tenant
	h       vtHeap  // 仅含非空租户
	sysVT   float64 // 系统虚拟时间：最近被选中任务开始服务时的 vt
	cmps    int     // 单次选择的比较次数（非导出计数器）
}

// New 创建空调度器。
func New() *Scheduler {
	s := &Scheduler{tenants: make(map[string]*tenant.Tenant)}
	s.h.cmps = &s.cmps
	return s
}

// Add 注册租户。
func (s *Scheduler) Add(t *tenant.Tenant) { s.tenants[t.ID] = t }

// Tenant 按 ID 查租户，未注册返回 nil。
func (s *Scheduler) Tenant(id string) *tenant.Tenant { return s.tenants[id] }

// Remove 移除租户；有排队任务或未注册时拒绝。空租户本就不在堆中，无需修堆。
func (s *Scheduler) Remove(id string) bool {
	t, ok := s.tenants[id]
	if !ok || t.Len() > 0 {
		return false
	}
	delete(s.tenants, id)
	return true
}

// Submit 入队一个任务；从空转为非空时把 vt 抬到 sysVT 后入堆。
func (s *Scheduler) Submit(tk task.Task) bool {
	t, ok := s.tenants[tk.Tenant]
	if !ok {
		return false
	}
	if t.Len() == 0 {
		t.LiftVT(s.sysVT)
		t.Enqueue(tk)
		heap.Push(&s.h, t)
	} else {
		t.Enqueue(tk)
	}
	return true
}

// Next 选出并取出下一个任务：vt 最小者优先，并列按 ID 字典序。
func (s *Scheduler) Next() (task.Task, bool) {
	s.cmps = 0
	if s.h.Len() == 0 {
		return task.Task{}, false
	}
	t := heap.Pop(&s.h).(*tenant.Tenant)
	if t.VT() > s.sysVT {
		s.sysVT = t.VT()
	}
	tk, _ := t.Dequeue()
	c := tk.Cost
	if c < MinCost {
		c = MinCost
	}
	t.Advance(c / t.Weight)
	if t.Len() > 0 {
		heap.Push(&s.h, t)
	}
	return tk, true
}

// Active 返回堆中（非空）租户数，空闲租户不占选择代价。
func (s *Scheduler) Active() int { return s.h.Len() }

// LastComparisons 返回最近一次 Next 的堆比较次数。
func (s *Scheduler) LastComparisons() int { return s.cmps }

// SysVT 返回当前系统虚拟时间。
func (s *Scheduler) SysVT() float64 { return s.sysVT }
