package ontology

// entry is one weighted candidate held in the reservoir.
type entry[T any] struct {
	value T
	key   float64 // u^(1/w); larger keys survive
	seq   uint64  // arrival order, used to make Sample output deterministic
}

// entryHeap is a min-heap by key: the root is the weakest survivor,
// which is the element evicted when a better candidate arrives.
// It implements container/heap.Interface.
type entryHeap[T any] []entry[T]

func (h entryHeap[T]) Len() int { return len(h) }

func (h entryHeap[T]) Less(i, j int) bool { return h[i].key < h[j].key }

func (h entryHeap[T]) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *entryHeap[T]) Push(x any) {
	*h = append(*h, x.(entry[T]))
}

func (h *entryHeap[T]) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}
