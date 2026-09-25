// Package check 提供切片模拟的朴素 FIFO 参照与对照验证，供测试使用。
package check

import (
	"fmt"

	"ontology/queue"
)

// Ref 是朴素参照队列：直接用一个切片维护 FIFO 顺序。
type Ref[T any] struct {
	items []T
}

// Enqueue 将 v 追加到队尾。
func (r *Ref[T]) Enqueue(v T) {
	r.items = append(r.items, v)
}

// Dequeue 移除并返回队首；空队返回 (零值, false)。
func (r *Ref[T]) Dequeue() (T, bool) {
	if len(r.items) == 0 {
		var zero T
		return zero, false
	}
	v := r.items[0]
	r.items = r.items[1:]
	return v, true
}

// Peek 返回队首但不出队；空队返回 (零值, false)。
func (r *Ref[T]) Peek() (T, bool) {
	if len(r.items) == 0 {
		var zero T
		return zero, false
	}
	return r.items[0], true
}

// Len 返回队列中元素个数。
func (r *Ref[T]) Len() int {
	return len(r.items)
}

// Verify 对 queue.Queue 与朴素参照施加同一操作序列并逐一对比。
// 操作编码：v>=0 入队 v；-1 出队；-2 窥视。不一致时返回描述且 ok=false。
func Verify(ops []int) (msg string, ok bool) {
	var q queue.Queue[int]
	var r Ref[int]
	for i, v := range ops {
		if v >= 0 {
			q.Enqueue(v)
			r.Enqueue(v)
			continue
		}
		qf, rf := q.Dequeue, r.Dequeue
		if v == -2 {
			qf, rf = q.Peek, r.Peek
		}
		qv, qok := qf()
		rv, rok := rf()
		if qv != rv || qok != rok {
			return fmt.Sprintf("op %d: got (%v,%v), want (%v,%v)", i, qv, qok, rv, rok), false
		}
	}
	if q.Len() != r.Len() {
		return fmt.Sprintf("len=%d, want %d", q.Len(), r.Len()), false
	}
	return "", true
}
