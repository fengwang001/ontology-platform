// Package tenant 维护单个租户的 FIFO 队列与虚拟时间账本。
package tenant

import "ontology/task"

// MinCost 是虚拟时间记账的最小代价下界，防止零代价任务使 vt 不推进。
const MinCost = 1e-9

// Tenant 是一个租户的队列与账本。
type Tenant struct {
	id     string
	weight float64
	vt     float64
	queue  []task.Task
}

// New 创建租户，权重非法时返回 task.ErrInvalidWeight。
func New(id string, weight float64) (*Tenant, error) {
	if err := task.ValidateWeight(weight); err != nil {
		return nil, err
	}
	return &Tenant{id: id, weight: weight}, nil
}

// ID 返回租户 ID。
func (t *Tenant) ID() string { return t.id }

// Weight 返回权重。
func (t *Tenant) Weight() float64 { return t.weight }

// VT 返回当前虚拟时间。
func (t *Tenant) VT() float64 { return t.vt }

// Len 返回排队任务数。
func (t *Tenant) Len() int { return len(t.queue) }

// Empty 报告队列是否为空。
func (t *Tenant) Empty() bool { return len(t.queue) == 0 }

// Push 入队一个任务。
func (t *Tenant) Push(j task.Task) {
	t.queue = append(t.queue, j)
}

// Pop 取出队首任务；队列空时 ok 为 false。
func (t *Tenant) Pop() (j task.Task, ok bool) {
	if len(t.queue) == 0 {
		return task.Task{}, false
	}
	j = t.queue[0]
	t.queue = t.queue[1:]
	return j, true
}

// Bump 在租户从空转为非空时把 vt 抬到 max(vt, now)，
// 防止长期空闲后以陈旧小值连续独占调度器。
func (t *Tenant) Bump(now float64) {
	if now > t.vt {
		t.vt = now
	}
}

// Advance 执行代价为 cost 的任务后推进 vt：vt += max(cost,MinCost)/weight。
func (t *Tenant) Advance(cost float64) {
	charge := cost
	if charge < MinCost {
		charge = MinCost
	}
	t.vt += charge / t.weight
}
