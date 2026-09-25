// Package check 提供朴素切片 FIFO 参照，用于对照验证 ring 的行为。
package check

// Ref 用切片模拟的朴素 FIFO，作为 ring.Buffer 的参照实现。
type Ref[T any] struct{ q []T }

func (r *Ref[T]) Enqueue(v T) { r.q = append(r.q, v) }

func (r *Ref[T]) Dequeue() (T, bool) {
	var zero T
	if len(r.q) == 0 {
		return zero, false
	}
	v := r.q[0]
	r.q = r.q[1:]
	return v, true
}

func (r *Ref[T]) Len() int { return len(r.q) }
