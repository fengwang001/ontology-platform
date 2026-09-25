// Package queue 用两个栈模拟 FIFO 队列，摊还 O(1)。
package queue

import (
	"errors"
	"sync"

	"ontology/stack"
)

// ErrDequeueEmpty 是空队出队的哨兵错误，可用 errors.Is 区分。
var ErrDequeueEmpty = errors.New("queue: dequeue on empty queue")

// Queue 由 in/out 两个栈组成：入队压 in，出队弹 out，
// 仅当 out 为空时才把 in 整体倒入 out（惰性转移）。
type Queue[T any] struct {
	mu    sync.Mutex
	in    stack.Stack[T]
	out   stack.Stack[T]
	moves int // 非导出计数器：元素移动总次数（入 in、随倾倒移动、出 out 各计 1）
}

// Enqueue 将 v 入队。
func (q *Queue[T]) Enqueue(v T) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.in.Push(v)
	q.moves++
}

// Dequeue 出队；空队返回 (零值, false)。
func (q *Queue[T]) Dequeue() (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	v, err := q.dequeueLocked()
	return v, err == nil
}

func (q *Queue[T]) dequeueLocked() (T, error) {
	q.pourLocked()
	v, err := q.out.Pop()
	if err != nil {
		var zero T
		return zero, ErrDequeueEmpty
	}
	q.moves++
	return v, nil
}

// pourLocked 仅在 out 为空时把 in 整体倒入 out。
func (q *Queue[T]) pourLocked() {
	if q.out.Len() > 0 {
		return
	}
	for q.in.Len() > 0 {
		v, _ := q.in.Pop()
		q.out.Push(v)
		q.moves++
	}
}

// Peek 返回队首但不出队；空队返回 (零值, false)。
func (q *Queue[T]) Peek() (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pourLocked()
	v, err := q.out.Peek()
	return v, err == nil
}

// Len 返回队列中元素个数。
func (q *Queue[T]) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.in.Len() + q.out.Len()
}

// Moves 返回元素移动总次数，用于验证摊还复杂度。
func (q *Queue[T]) Moves() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.moves
}
