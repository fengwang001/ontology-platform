// Package check provides a naive slice-based FIFO used as the
// reference model for testing the ring buffer.
package check

// Ref is an unbounded FIFO queue backed by a plain slice.
type Ref[T any] struct{ items []T }

func (r *Ref[T]) Enqueue(v T) { r.items = append(r.items, v) }

func (r *Ref[T]) Dequeue() (T, bool) {
	var zero T
	if len(r.items) == 0 {
		return zero, false
	}
	v := r.items[0]
	r.items = r.items[1:]
	return v, true
}

func (r *Ref[T]) Len() int { return len(r.items) }
