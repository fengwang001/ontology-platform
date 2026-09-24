// Package mono 实现单调队列：候选值从队首到队尾非严格递减，
// 队首即当前窗口最大值。依赖 deque。
package mono

import (
	"errors"

	"ontology/deque"
)

// ErrBadIndex 表示 Push 的下标未严格递增（重复或回退）。
var ErrBadIndex = errors.New("mono: push index not strictly increasing")

var (
	errNotMono   = errors.New("mono: candidates not non-increasing")
	errHeadRange = errors.New("mono: front index outside window")
	errTooLong   = errors.New("mono: more candidates than window width")
	errNonLinear = errors.New("mono: enqueue/dequeue ops exceed 2 per element")
)

// Queue 是单调队列本体。ops 为非导出计数器，记录入队与出队总次数。
type Queue struct {
	dq      deque.Deque
	ops     int
	pushed  int
	last    int
	started bool
}

// Push 推入第 i 个元素（值 v）。下标必须严格递增，否则返回
// ErrBadIndex 且队列内容不变。队尾严格更小者被弹出，相等者保留。
func (q *Queue) Push(i, v int) error {
	if q.started && i <= q.last {
		return ErrBadIndex
	}
	for q.dq.Len() > 0 && q.dq.Back().Value < v {
		q.dq.PopBack()
		q.ops++
	}
	q.dq.PushBack(deque.Item{Index: i, Value: v})
	q.ops++
	q.pushed++
	q.last = i
	q.started = true
	return nil
}

// Evict 淘汰下标小于 left 的过期候选。
func (q *Queue) Evict(left int) {
	for q.dq.Len() > 0 && q.dq.FrontIndex() < left {
		q.dq.PopFront()
		q.ops++
	}
}

// Max 返回当前最大值（队首）；队列为空时 ok 为 false。
func (q *Queue) Max() (max int, ok bool) {
	if q.dq.Len() == 0 {
		return 0, false
	}
	return q.dq.Front().Value, true
}

// Verify 自检：候选非严格递减、队首下标在 [lo,hi] 内、
// 长度不超过 w、入出队总次数不超过每元素两次。
func (q *Queue) Verify(lo, hi, w int) error {
	switch {
	case q.dq.Len() > w:
		return errTooLong
	case q.ops > 2*q.pushed:
		return errNonLinear
	case q.dq.Len() == 0:
		return nil
	}
	if fi := q.dq.FrontIndex(); fi < lo || fi > hi {
		return errHeadRange
	}
	for k := 1; k < q.dq.Len(); k++ {
		if q.dq.At(k-1).Value < q.dq.At(k).Value {
			return errNotMono
		}
	}
	return nil
}
