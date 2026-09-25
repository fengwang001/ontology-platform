package queue

import (
	"sync"

	"ontology/stack"
)

type Queue[T any] struct {
	mu        sync.RWMutex
	in        *stack.Stack[T]
	out       *stack.Stack[T]
	moves     int
	lastMoves int
}

func New[T any]() *Queue[T] {
	return &Queue[T]{
		in:  stack.New[T](),
		out: stack.New[T](),
	}
}

func (q *Queue[T]) Enqueue(v T) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.lastMoves = 0
	q.in.Push(v)
}

func (q *Queue[T]) Dequeue() (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.lastMoves = 0
	if q.out.Len() == 0 {
		for q.in.Len() > 0 {
			v, _ := q.in.Pop()
			q.out.Push(v)
			q.moves++
			q.lastMoves++
		}
	}
	return q.out.Pop()
}

func (q *Queue[T]) Peek() (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.out.Len() == 0 {
		for q.in.Len() > 0 {
			v, _ := q.in.Pop()
			q.out.Push(v)
			q.moves++
			q.lastMoves++
		}
	}
	return q.out.Peek()
}

func (q *Queue[T]) Len() int {
	q.mu.RLock()
	defer q.mu.RUnlock()

	return q.in.Len() + q.out.Len()
}

func (q *Queue[T]) Moves() int {
	q.mu.RLock()
	defer q.mu.RUnlock()

	return q.moves
}

func (q *Queue[T]) LastMoves() int {
	q.mu.RLock()
	defer q.mu.RUnlock()

	return q.lastMoves
}
