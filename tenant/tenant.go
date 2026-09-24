// Package tenant 维护单个租户的 FIFO 任务队列与虚拟时间账本。
package tenant

import (
	"errors"

	"ontology/task"
)

// ErrInvalidWeight 权重必须为正；ErrInvalidCost 代价不可为负。
var (
	ErrInvalidWeight = errors.New("tenant: weight must be > 0")
	ErrInvalidCost   = errors.New("tenant: cost must be >= 0")
)

// Tenant 是一个租户的调度状态。调度器持锁访问，故自身不加锁。
type Tenant struct {
	id     string
	weight float64
	vt     float64
	q      []task.Task

	// inHeap 标记是否已在候选堆中（队列从空变非空时入堆）。
	inHeap bool
	// gone 标记已移除：拒绝新入队。
	gone bool
}

// New 创建租户并校验权重。
func New(id string, weight float64) (*Tenant, error) {
	if !(weight > 0) { // NaN 与 <=0 一并拒绝
		return nil, ErrInvalidWeight
	}
	return &Tenant{id: id, weight: weight}, nil
}

func (t *Tenant) ID() string      { return t.id }
func (t *Tenant) Weight() float64 { return t.weight }

// VT 当前虚拟时间。
func (t *Tenant) VT() float64 { return t.vt }

// Len 队列长度。
func (t *Tenant) Len() int { return len(t.q) }

// InHeap / SetInHeap 维护"队列非空即在堆中"的不变量。
func (t *Tenant) InHeap() bool      { return t.inHeap }
func (t *Tenant) SetInHeap(v bool)  { t.inHeap = v }

// WasEmpty 报告入队前是否为空（用于触发 vt 抬升与入堆）。
func (t *Tenant) WasEmpty() bool { return len(t.q) == 0 }

// RaiseVT 在从空转非空前把 vt 抬到系统虚拟时间（见 DESIGN 第 2 节）。
func (t *Tenant) RaiseVT(gvt float64) {
	if gvt > t.vt {
		t.vt = gvt
	}
}

// Enqueue 追加任务，校验代价与存活状态。
func (t *Tenant) Enqueue(task task.Task) error {
	if t.gone {
		return ErrTenantGone
	}
	if task.Cost() < 0 {
		return ErrInvalidCost
	}
	t.q = append(t.q, task)
	return nil
}

// Dequeue 取出队首；空队列返回零值任务与 false。
func (t *Tenant) Dequeue() (task.Task, bool) {
	if len(t.q) == 0 {
		return task.Task{}, false
	}
	head := t.q[0]
	t.q = t.q[1:]
	return head, true
}

// Advance 执行代价 cost 后按 c/w 推进虚拟时间。
func (t *Tenant) Advance(cost float64) { t.vt += cost / t.weight }

// MarkGone / Gone 支持租户移除。
func (t *Tenant) MarkGone()    { t.gone = true }
func (t *Tenant) Gone() bool   { return t.gone }

// ErrTenantGone 在租户已被移除后入队时返回（sched 层复用同一错误）。
var ErrTenantGone = errors.New("tenant: tenant removed")
