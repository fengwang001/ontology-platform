// Package tenant 维护单个租户的 FIFO 队列与虚拟时间账本。
package tenant

import "ontology/task"

// Tenant 记录一个租户的权重、虚拟时间与排队任务。
// 不提供并发安全，由上层（sched/admit）串行化访问。
type Tenant struct {
	ID     string
	Weight float64
	vt     float64
	queue  []task.Task
}

// New 创建租户；权重合法性由 admit 层校验。
func New(id string, weight float64) *Tenant {
	return &Tenant{ID: id, Weight: weight}
}

// VT 返回当前虚拟时间。
func (t *Tenant) VT() float64 { return t.vt }

// LiftVT 把 vt 抬到 max(vt, sys)：空闲重入时对齐系统虚拟时间。
func (t *Tenant) LiftVT(sys float64) {
	if sys > t.vt {
		t.vt = sys
	}
}

// Advance 推进虚拟时间，delta 由调用方按 max(cost,MinCost)/Weight 算好。
func (t *Tenant) Advance(delta float64) { t.vt += delta }

// Enqueue 队尾入队。
func (t *Tenant) Enqueue(tk task.Task) { t.queue = append(t.queue, tk) }

// Dequeue 队头出队；空队列返回 ok=false。
func (t *Tenant) Dequeue() (tk task.Task, ok bool) {
	if len(t.queue) == 0 {
		return task.Task{}, false
	}
	tk = t.queue[0]
	t.queue = t.queue[1:]
	return tk, true
}

// Len 返回排队任务数。
func (t *Tenant) Len() int { return len(t.queue) }
