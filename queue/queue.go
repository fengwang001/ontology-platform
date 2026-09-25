// Package queue 用两个栈实现 FIFO 队列，出队时惰性整体转移（摊还 O(1)）。
package queue

import (
	"errors"
	"sync"

	"ontology/stack"
)

var (
	// ErrNilReceiver 在未初始化队列上调用方法时返回。
	ErrNilReceiver = errors.New("queue: nil receiver")
)

// Queue 由 in/out 两栈组成：Enqueue 入 in，Dequeue/Peek 取自 out。
type Queue[T any] struct {
	mu          sync.RWMutex
	in, out     *stack.Stack[T]
	moves, last int
}

func New[T any]() *Queue[T] {
	in, _ := stack.New[T](0)
	out, _ := stack.New[T](0)
	return &Queue[T]{sync.RWMutex{}, in, out, 0, 0}
}

// Enqueue 把 v 放入队尾，O(1) 且不触发栈间转移。
func (q *Queue[T]) Enqueue(v T) error {
	if q == nil {
		return ErrNilReceiver
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.last = 0
	return q.in.Push(v)
}

func (q *Queue[T]) Dequeue() (T, bool) {
	var zero T
	if q == nil {
		return zero, false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.head(true)
}

func (q *Queue[T]) Peek() (T, bool) {
	var zero T
	if q == nil {
		return zero, false
	}
	q.mu.RLock()
	nonEmpty := q.in.Len()+q.out.Len() > 0
	q.mu.RUnlock()
	if !nonEmpty {
		return zero, false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.head(false)
}

// head 须在持写锁时调用。
func (q *Queue[T]) head(pop bool) (T, bool) {
	var zero T
	q.last = 0
	if q.out.Len() == 0 {
		for q.in.Len() > 0 {
			v, _ := q.in.Pop()
			_ = q.out.Push(v)
			q.moves++
			q.last++
		}
	}
	if q.out.Len() == 0 {
		return zero, false
	}
	if pop {
		v, _ := q.out.Pop()
		return v, true
	}
	v, _ := q.out.Peek()
	return v, true
}

func (q *Queue[T]) Len() int {
	if q == nil {
		return 0
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.in.Len() + q.out.Len()
}

func (q *Queue[T]) Moves() int     { q.mu.RLock(); defer q.mu.RUnlock(); return q.moves }
func (q *Queue[T]) LastMoves() int { q.mu.RLock(); defer q.mu.RUnlock(); return q.last }
func (q *Queue[T]) ResetMoves()    { q.mu.Lock(); q.moves = 0; q.mu.Unlock() }
