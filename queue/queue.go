// Package queue 用两个栈实现 FIFO 队列，摊还 O(1)。
package queue

import (
	"sync"

	"ontology/stack"
)

// Queue 由 in/out 两个栈模拟队列：入队压 in，出队从 out 弹，
// 仅当 out 为空时才把 in 整体倒入 out（惰性转移）。
type Queue[T any] struct {
	mu    sync.Mutex
	in    stack.Stack[T]
	out   stack.Stack[T]
	moves int
}

// Enqueue 入队，把 v 压入 in 栈。
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
	q.shift()
	v, err := q.out.Pop()
	if err != nil {
		var zero T
		return zero, false
	}
	return v, true
}

// Peek 返回队首但不出队；空队返回 (零值, false)。
func (q *Queue[T]) Peek() (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.shift()
	v, err := q.out.Peek()
	if err != nil {
		var zero T
		return zero, false
	}
	return v, true
}

// Len 返回队列中的元素个数。
func (q *Queue[T]) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.in.Len() + q.out.Len()
}

// Moves 返回累计元素移动次数（入 in 计 1、倒出计 1），
// 用于验证摊还上界：n 次操作总移动数不超过 2n。
func (q *Queue[T]) Moves() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.moves
}

// shift 仅在 out 为空时把 in 整体倒入 out；调用方须持锁。
func (q *Queue[T]) shift() {
	if q.out.Len() > 0 {
		return
	}
	for {
		v, err := q.in.Pop()
		if err != nil {
			return
		}
		q.out.Push(v)
		q.moves++
	}
}
