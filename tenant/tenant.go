// Package tenant 管理每个租户的任务队列与虚拟时间账本。
package tenant

import (
	"errors"

	"ontology/task"
)

// Epsilon 是有效代价的最小下界，防止零代价任务无限独占。
// 取 1e-3：对单位代价任务，1000 个零成本任务累计仅 1 个 vt 单位，
// 长期份额扭曲 <0.1%，但零成本流会与正常流交替而不会整批独占。
const Epsilon = 1e-3

// ErrBadWeight 在权重非正时返回。
var ErrBadWeight = errors.New("tenant: weight must be positive")

// Tenant 是一个租户的 FIFO 队列与虚拟时间状态（非并发安全，由上层加锁）。
type Tenant struct {
	id     string
	weight float64
	vt     float64
	queue  []task.Task
}

// New 构造租户；weight 必须为正。
func New(id string, weight float64) (*Tenant, error) {
	if weight <= 0 {
		return nil, ErrBadWeight
	}
	return &Tenant{id: id, weight: weight}, nil
}

func (t *Tenant) ID() string     { return t.id }
func (t *Tenant) Weight() float64 { return t.weight }
func (t *Tenant) VT() float64    { return t.vt }
func (t *Tenant) Len() int       { return len(t.queue) }

// Enqueue 在队尾追加任务。
func (t *Tenant) Enqueue(task task.Task) { t.queue = append(t.queue, task) }

// Dequeue 取出队首任务；空队列返回 ok=false。
func (t *Tenant) Dequeue() (task.Task, bool) {
	if len(t.queue) == 0 {
		return task.Task{}, false
	}
	task := t.queue[0]
	t.queue = t.queue[1:]
	return task, true
}

// RaiseTo 把虚拟时间抬到 max(vt, floor)，用于空闲租户重新加入。
func (t *Tenant) RaiseTo(floor float64) {
	if floor > t.vt {
		t.vt = floor
	}
}

// Advance 执行代价为 cost 的任务后推进 vt：Δ = max(cost, Epsilon)/weight。
// Epsilon 是与权重无关的绝对最小有效代价：c=0 时 Δ=ε/w 虽小但为严格正数，
// 提交正常代价（≫ε）任务的其他租户至多在约 cost/(ε/w) 步内夺回调度权，
// 零代价租户不可能无限独占；ε 对正常任务的长期份额影响可忽略。
func (t *Tenant) Advance(cost float64) {
	charge := cost
	if charge < Epsilon {
		charge = Epsilon
	}
	t.vt += charge / t.weight
}
