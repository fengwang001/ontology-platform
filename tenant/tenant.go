// Package tenant 维护单个租户的等待队列与虚拟时间账本。
package tenant

import "ontology/task"

// Tenant 记录一个租户的权重、虚拟时间与先进先出等待队列。
// 不是并发安全的，并发访问由上层调度器加锁保护。
type Tenant struct {
	// ID 为租户标识，同时用于同虚拟时间时的字典序并列打破。
	ID string
	// Weight 为租户权重，必须为正；份额与权重成正比。
	Weight float64
	// VT 为租户的虚拟时间账本：每执行一个代价为 c 的任务，
	// VT 增加 c/Weight（见 DESIGN.md 第 1 节）。
	VT float64

	queue []task.Task
}

// New 创建一个空队列租户。
func New(id string, weight float64) *Tenant {
	return &Tenant{ID: id, Weight: weight}
}

// Push 将任务追加到等待队列尾部。
func (t *Tenant) Push(tk task.Task) {
	t.queue = append(t.queue, tk)
}

// Pop 取出队首任务。调用前必须保证队列非空。
func (t *Tenant) Pop() task.Task {
	tk := t.queue[0]
	t.queue = t.queue[1:]
	return tk
}

// Empty 报告等待队列是否为空。
func (t *Tenant) Empty() bool {
	return len(t.queue) == 0
}

// Len 返回等待队列长度。
func (t *Tenant) Len() int {
	return len(t.queue)
}

// Advance 按有效代价推进虚拟时间：VT += cost/Weight。
func (t *Tenant) Advance(cost float64) {
	t.VT += cost / t.Weight
}

// Lift 将虚拟时间抬升到不低于 sysVT（见 DESIGN.md 第 2 节）。
func (t *Tenant) Lift(sysVT float64) {
	if t.VT < sysVT {
		t.VT = sysVT
	}
}
