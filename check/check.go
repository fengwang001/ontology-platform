package check

import (
	"errors"
	"math/rand"

	"ontology/queue"
)

var (
	ErrEmptyQueue      = errors.New("queue is empty")
	ErrFIFOMismatch    = errors.New("fifo order mismatch")
	ErrAmortizedBudget = errors.New("amortized move budget exceeded")
)

type Reference[T any] struct {
	items []T
}

func NewReference[T any]() *Reference[T] {
	return &Reference[T]{}
}

func (r *Reference[T]) Enqueue(v T) {
	r.items = append(r.items, v)
}

func (r *Reference[T]) Dequeue() (T, bool) {
	if len(r.items) == 0 {
		var zero T
		return zero, false
	}
	v := r.items[0]
	r.items = r.items[1:]
	return v, true
}

func (r *Reference[T]) Peek() (T, bool) {
	if len(r.items) == 0 {
		var zero T
		return zero, false
	}
	return r.items[0], true
}

func (r *Reference[T]) Len() int {
	return len(r.items)
}

type BadQueue struct{ items []int; Moves int }

func (b *BadQueue) Add(v int) { b.items = append(b.items, v) }
func (b *BadQueue) Get() (int, bool) {
	if len(b.items) == 0 {
		return 0, false
	}
	v := b.items[0]
	b.items = b.items[1:]
	b.Moves = len(b.items) + 1
	return v, true
}

func EqualStep(q *queue.Queue[int], r *Reference[int]) bool {
	g, gok := q.Dequeue()
	w, wok := r.Dequeue()
	return g == w && gok == wok && q.Len() == r.Len()
}

func RandomWorkload(n int64) (*queue.Queue[int], *Reference[int], int, int) {
	q, r := queue.New[int](), NewReference[int]()
	rnd := rand.New(rand.NewSource(n))
	for i := 0; i < 10000; i++ {
		if rnd.Intn(2) == 0 || r.Len() == 0 {
			q.Enqueue(i)
			r.Enqueue(i)
		} else if !EqualStep(q, r) {
			return q, r, i, q.Moves()
		}
	}
	return q, r, 10000, q.Moves()
}
