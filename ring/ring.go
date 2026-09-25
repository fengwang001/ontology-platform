// Package ring implements a fixed-capacity ring buffer.
package ring

import "errors"

var (
	ErrFull   = errors.New("ring: buffer full")
	ErrBadCap = errors.New("ring: capacity must be positive")
)

// Ring is a fixed-capacity FIFO buffer. It is not safe for
// concurrent use; callers must synchronize externally.
type Ring[T any] struct {
	buf  []T
	r, w int
	size int
	peak int
}

// New returns a Ring holding at most cap elements.
func New[T any](cap int) (*Ring[T], error) {
	if cap <= 0 {
		return nil, ErrBadCap
	}
	return &Ring[T]{buf: make([]T, cap)}, nil
}

// Enqueue appends v; it returns ErrFull without side effects when full.
func (q *Ring[T]) Enqueue(v T) error {
	if q.size == len(q.buf) {
		return ErrFull
	}
	q.buf[q.w] = v
	q.w = (q.w + 1) % len(q.buf)
	q.size++
	if q.size > q.peak {
		q.peak = q.size
	}
	return nil
}

// Dequeue removes the oldest element, or returns (zero, false) if empty.
func (q *Ring[T]) Dequeue() (T, bool) {
	var zero T
	if q.size == 0 {
		return zero, false
	}
	v := q.buf[q.r]
	q.buf[q.r] = zero
	q.r = (q.r + 1) % len(q.buf)
	q.size--
	return v, true
}

func (q *Ring[T]) Len() int   { return q.size }
func (q *Ring[T]) Cap() int   { return len(q.buf) }
func (q *Ring[T]) Full() bool { return q.size == len(q.buf) }
func (q *Ring[T]) Empty() bool {
	return q.size == 0
}

// Peak returns the historical maximum number of buffered elements.
func (q *Ring[T]) Peak() int { return q.peak }
