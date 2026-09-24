// Package mono 在 deque 之上实现单调双端队列，维护滑动窗口的最大值候选。
package mono

import (
	"errors"

	"ontology/deque"
)

// ErrIndexNotIncreasing 表示 Push 收到的下标未严格递增。
var ErrIndexNotIncreasing = errors.New("mono: push index must be strictly increasing")

type entry struct {
	index int
	value int
}

// Queue 是候选的单调队列：队首到队尾值非严格递减，队首即当前最大候选。
type Queue struct {
	dq      *deque.Deque[entry]
	lastIdx int
	pushed  int
	ops     int // 非导出计数器：入队与出队的总次数
}

// New 创建空单调队列。
func New() *Queue {
	return &Queue{dq: deque.New[entry](), lastIdx: -1}
}

// Len 返回候选个数。
func (q *Queue) Len() int { return q.dq.Len() }

// Push 推入下标 i、值 v 的元素；下标必须严格递增，否则拒绝且不改变任何状态。
func (q *Queue) Push(i, v int) error {
	if i <= q.lastIdx {
		return ErrIndexNotIncreasing
	}
	for q.dq.Len() > 0 {
		tail, _ := q.dq.At(q.dq.Len() - 1)
		if tail.value >= v {
			break // 相等保留：只弹严格更小的队尾，保证候选不早退
		}
		q.dq.PopBack()
		q.ops++
	}
	q.dq.PushBack(entry{index: i, value: v})
	q.ops++
	q.pushed++
	q.lastIdx = i
	return nil
}

// Expire 淘汰所有下标 < left 的队首候选，即已滑出窗口左边界的元素。
func (q *Queue) Expire(left int) {
	for q.dq.Len() > 0 {
		front, _ := q.dq.Front()
		if front.index >= left {
			break
		}
		q.dq.PopFront()
		q.ops++
	}
}

// Max 返回队首候选的下标与值；队列空时 ok 为 false。
func (q *Queue) Max() (index, value int, ok bool) {
	front, exists := q.dq.Front()
	if !exists {
		return 0, 0, false
	}
	return front.index, front.value, true
}

// Healthy 核验：候选值非严格递减、队首下标 >= left、队长 <= width、入出队总次
// 数 <= 2*已推入元素数（每个元素至多入队一次、出队一次）。
func (q *Queue) Healthy(left, width int) bool {
	if q.dq.Len() > width {
		return false
	}
	if front, ok := q.dq.Front(); ok && front.index < left {
		return false
	}
	for p := 1; p < q.dq.Len(); p++ {
		prev, _ := q.dq.At(p - 1)
		cur, _ := q.dq.At(p)
		if cur.index <= prev.index || cur.value > prev.value {
			return false
		}
	}
	return q.ops <= 2*q.pushed
}
