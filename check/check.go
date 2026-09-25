package check

import "cmp"

type Naive[T cmp.Ordered] struct {
	vals []T
}

func NewNaive[T cmp.Ordered]() *Naive[T] {
	return &Naive[T]{}
}

func (n *Naive[T]) Push(val T) int {
	return 0
}

func (n *Naive[T]) Pop() (T, bool) {
	var zero T
	return zero, false
}

func (n *Naive[T]) DecreaseKey(id int, val T) error {
	return nil
}

func (n *Naive[T]) Len() int {
	return 0
}
