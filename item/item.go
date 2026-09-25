package item

import "ontology/heap"

type Heap = heap.Heap[int]
type Item = heap.Entry[int]

var (
	ErrGone       = heap.ErrGone
	ErrUnknown    = heap.ErrUnknown
	ErrNotSmaller = heap.ErrNotSmaller
)

func New() *Heap { return heap.New[int]() }
