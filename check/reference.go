// Package check 提供朴素 FIFO 参照实现，供测试与 ring 逐拍对比。
package check

import "ontology/ring"

var (
	ErrBadCap = ring.ErrBadCap
	ErrFull   = ring.ErrFull
	ErrEmpty  = ring.ErrEmpty
)

// Naive 用一个切片模拟有界 FIFO，语义直白、无指针绕回。
type Naive[T any] struct {
	data []T
	cap  int
}

func NewNaive[T any](capacity int) (*Naive[T], error) {
	if capacity <= 0 {
		return nil, ErrBadCap
	}
	return &Naive[T]{data: make([]T, 0, capacity), cap: capacity}, nil
}

func (n *Naive[T]) Enqueue(v T) error {
	if len(n.data) == n.cap {
		return ErrFull
	}
	n.data = append(n.data, v)
	return nil
}

func (n *Naive[T]) Dequeue() (T, bool) {
	var zero T
	if len(n.data) == 0 {
		return zero, false
	}
	v := n.data[0]
	n.data = n.data[1:]
	return v, true
}

func (n *Naive[T]) Len() int { return len(n.data) }
