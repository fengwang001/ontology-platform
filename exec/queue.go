package exec

import (
	"container/heap"

	"ontology/action"
)

// priorityQueue 按（有效优先级降序，seq 升序）排列在队操作。
// pos 记录每个在队操作的堆下标，使附着/取消后的优先级变化可就地 Fix，
// 且丢失回队时 seq 不变即可按原序号归位。
type priorityQueue struct {
	items []*action.Op
	pos   map[*action.Op]int
}

func newPriorityQueue() *priorityQueue {
	return &priorityQueue{pos: make(map[*action.Op]int)}
}

func (q *priorityQueue) Len() int { return len(q.items) }

func (q *priorityQueue) Less(i, j int) bool {
	a, b := q.items[i], q.items[j]
	pa, pb := a.EffectivePrio(), b.EffectivePrio()
	if pa != pb {
		return pa > pb
	}
	return a.Seq < b.Seq
}

func (q *priorityQueue) Swap(i, j int) {
	q.items[i], q.items[j] = q.items[j], q.items[i]
	q.pos[q.items[i]] = i
	q.pos[q.items[j]] = j
}

func (q *priorityQueue) Push(x any) {
	o := x.(*action.Op)
	q.pos[o] = len(q.items)
	q.items = append(q.items, o)
}

func (q *priorityQueue) Pop() any {
	n := len(q.items)
	o := q.items[n-1]
	q.items = q.items[:n-1]
	delete(q.pos, o)
	return o
}

func (q *priorityQueue) push(o *action.Op) {
	o.State = action.Queued
	heap.Push(q, o)
}

func (q *priorityQueue) pop() *action.Op {
	if len(q.items) == 0 {
		return nil
	}
	return heap.Pop(q).(*action.Op)
}

func (q *priorityQueue) contains(o *action.Op) bool {
	_, ok := q.pos[o]
	return ok
}

// fix 按操作新的有效优先级就地调整堆位置。
func (q *priorityQueue) fix(o *action.Op) {
	if i, ok := q.pos[o]; ok {
		heap.Fix(q, i)
	}
}

func (q *priorityQueue) remove(o *action.Op) {
	if i, ok := q.pos[o]; ok {
		heap.Remove(q, i)
	}
}

// order 返回当前队列摘要次序（测试/可复现校验用，O(n log n) 拷贝）。
func (q *priorityQueue) order() []string {
	cp := &priorityQueue{
		items: append([]*action.Op(nil), q.items...),
		pos:   make(map[*action.Op]int, len(q.pos)),
	}
	for k, v := range q.pos {
		cp.pos[k] = v
	}
	out := make([]string, 0, cp.Len())
	for cp.Len() > 0 {
		out = append(out, cp.pop().Digest)
	}
	return out
}
