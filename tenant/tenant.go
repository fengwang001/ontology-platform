// Package tenant 维护单个租户的 FIFO 任务队列与虚拟时间账本。
package tenant

import (
	"errors"
	"math"

	"ontology/task"
)

// ErrBadWeight 表示权重非法：NaN/Inf、非正或超出允许区间。
var ErrBadWeight = errors.New("tenant: invalid weight")

const (
	// MinWeight 防止 c/w 溢出 float64。
	MinWeight = 1e-6
	// MaxWeight 与任务代价上界对齐。
	MaxWeight = 1e100
	// MinCost 是虚拟时间记账的最小代价下界，防止 0 代价任务使 vt
	// 不推进而被同一租户无限独占（见 DESIGN.md 第 4 节）。
	MinCost = 1e-9
)

// Tenant 是一个租户的队列与账本。本身不加锁；并发安全由 sched 保证。
type Tenant struct {
	id     string
	weight float64
	vt     float64
	queue  []task.Task
}

// ValidWeight 判定权重合法：有限正数且落在 [MinWeight, MaxWeight]。
func ValidWeight(w float64) bool {
	return !math.IsNaN(w) && !math.IsInf(w, 0) && w >= MinWeight && w <= MaxWeight
}

// New 创建租户，权重非法时返回 ErrBadWeight。
func New(id string, weight float64) (*Tenant, error) {
	if !ValidWeight(weight) {
		return nil, ErrBadWeight
	}
	return &Tenant{id: id, weight: weight}, nil
}

// ID 返回租户标识。
func (t *Tenant) ID() string { return t.id }

// Weight 返回租户权重。
func (t *Tenant) Weight() float64 { return t.weight }

// VT 返回当前虚拟时间。
func (t *Tenant) VT() float64 { return t.vt }

// Len 返回排队任务数。
func (t *Tenant) Len() int { return len(t.queue) }

// Empty 报告队列是否为空。
func (t *Tenant) Empty() bool { return len(t.queue) == 0 }

// RaiseVT 在租户由空转非空时把 vt 抬到 max(vt, now)（系统虚拟时间）。
func (t *Tenant) RaiseVT(now float64) {
	if now > t.vt {
		t.vt = now
	}
}

// Enqueue 追加一个任务到队尾。
func (t *Tenant) Enqueue(tk task.Task) {
	t.queue = append(t.queue, tk)
}

// Dequeue 取出队首任务；空队列时 ok 为 false。
func (t *Tenant) Dequeue() (tk task.Task, ok bool) {
	if len(t.queue) == 0 {
		return task.Task{}, false
	}
	tk = t.queue[0]
	t.queue = t.queue[1:]
	return tk, true
}

// Charge 在执行代价 c 后推进虚拟时间：vt += max(c, MinCost) / weight。
func (t *Tenant) Charge(c float64) {
	if c < MinCost {
		c = MinCost
	}
	t.vt += c / t.weight
}
