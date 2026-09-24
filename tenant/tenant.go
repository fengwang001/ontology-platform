// Package tenant 维护单个租户的 FIFO 队列、权重与虚拟时间账本。
package tenant

import (
	"errors"

	"ontology/task"
)

// ErrBadWeight 表示租户权重为 0 或负数。
var ErrBadWeight = errors.New("tenant: weight must be positive")

// MinCost 是零代价任务的有效代价下界（DESIGN.md 第 4 节）。
// 取 1e-3：零代价租户相对单位代价租户的连续执行步数被限定在
// 1/MinCost = 1000 步以内，是有限且可实测的，而非无限独占。
const MinCost = 1e-3

// Queue 是单个租户的队列与虚拟时间账本。
//
// 非并发安全：由 sched 包在持锁状态下调用。
type Queue struct {
	id     string
	weight float64
	tasks  []task.Task
	vt     float64
}

// NewQueue 以给定正权重创建租户队列。
func NewQueue(id string, weight float64) (*Queue, error) {
	if weight <= 0 {
		return nil, ErrBadWeight
	}
	return &Queue{id: id, weight: weight}, nil
}

// ID 返回租户 ID。
func (q *Queue) ID() string { return q.id }

// Weight 返回租户权重。
func (q *Queue) Weight() float64 { return q.weight }

// VT 返回当前虚拟时间。
func (q *Queue) VT() float64 { return q.vt }

// Len 返回排队任务数。
func (q *Queue) Len() int { return len(q.tasks) }

// Empty 报告队列是否为空。
func (q *Queue) Empty() bool { return len(q.tasks) == 0 }

// Push 将任务追加到 FIFO 队尾。
func (q *Queue) Push(t task.Task) { q.tasks = append(q.tasks, t) }

// Pop 移除并返回队首任务；队列空时 ok 为 false。
func (q *Queue) Pop() (t task.Task, ok bool) {
	if len(q.tasks) == 0 {
		return task.Task{}, false
	}
	t = q.tasks[0]
	q.tasks = q.tasks[1:]
	return t, true
}

// Rejoin 在队列由空转非空时把 vt 抬到 max(vt, sysVT)，
// 避免空闲租户以陈旧小值重新加入后连续独占（DESIGN.md 第 3 节）。
// 非空队列重复调用不产生任何影响。
func (q *Queue) Rejoin(emptyBefore bool, sysVT float64) {
	if emptyBefore && sysVT > q.vt {
		q.vt = sysVT
	}
}

// Advance 按有效代价推进虚拟时间：vt += max(cost, MinCost)/weight。
// 结果做饱和钳制，防止极端权重下溢出为 Inf/NaN（DESIGN.md 第 5 节）。
func (q *Queue) Advance(cost float64) float64 {
	c := cost
	if c < MinCost {
		c = MinCost
	}
	q.vt = saturate(q.vt + c/q.weight)
	return q.vt
}

func saturate(v float64) float64 {
	const cap = 1e150
	if v != v || v > cap || v < -cap { // v != v 捕获 NaN
		return cap
	}
	return v
}
