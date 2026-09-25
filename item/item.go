package item

import "ontology/heap"

type Item[T any] = heap.Entry[T]

var (
	ErrUnknown    = heap.ErrUnknown
	ErrGone       = heap.ErrGone
	ErrNotSmaller = heap.ErrNotSmaller
)
