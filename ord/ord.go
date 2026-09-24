// Package ord defines the element type, its deterministic total order
// (score desc, then id asc) and a generic binary-heap primitive. It
// depends on nothing else in this module.
package ord

// Elem is a scored element.
type Elem struct {
	ID    string
	Score int64
}

// Less reports whether a outranks b: higher score first; ties broken by
// smaller id (lexicographic). It is a strict, deterministic total order:
// for distinct elements exactly one of Less(a,b)/Less(b,a) holds.
func Less(a, b Elem) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.ID < b.ID
}

// Heap is a binary heap over arbitrary payload type T with a caller-supplied
// order. Pass Less on Elem for a best-first heap (root = best = next
// backfiller), or the reversed comparison for a worst-first heap (root =
// threshold element). onStep, when non-nil, fires once per comparison and
// once per swap, so an enclosing package can count heap work.
type Heap[T any] struct {
	data   []T
	less   func(a, b T) bool
	onStep func(int)
}

// NewHeap constructs an empty heap with the given order and step callback.
func NewHeap[T any](less func(a, b T) bool, onStep func(int)) *Heap[T] {
	return &Heap[T]{less: less, onStep: onStep}
}

func (h *Heap[T]) tick(n int) {
	if h.onStep != nil {
		h.onStep(n)
	}
}

// Len reports the number of physical entries (stale entries included).
func (h *Heap[T]) Len() int { return len(h.data) }

// Peek returns the root. Caller must ensure Len > 0.
func (h *Heap[T]) Peek() T { return h.data[0] }

// Push inserts e and restores the heap property (O(log n)).
func (h *Heap[T]) Push(e T) {
	h.tick(1) // one push
	h.data = append(h.data, e)
	h.up(len(h.data) - 1)
}

// Pop removes and returns the root and restores the heap property.
// Caller must ensure Len > 0.
func (h *Heap[T]) Pop() T {
	h.tick(1) // one pop
	root := h.data[0]
	n := len(h.data) - 1
	if n > 0 {
		h.data[0] = h.data[n]
		h.data = h.data[:n]
		h.down(0, n)
	} else {
		h.data = h.data[:0]
	}
	return root
}

// Snapshot returns a copy of every physical entry in storage order.
// It performs no comparisons and is not counted by onStep.
func (h *Heap[T]) Snapshot() []T {
	return append([]T(nil), h.data...)
}

func (h *Heap[T]) up(j int) {
	for j > 0 {
		p := (j - 1) / 2
		h.tick(1) // compare child with parent
		if !h.less(h.data[j], h.data[p]) {
			return
		}
		h.data[p], h.data[j] = h.data[j], h.data[p]
		h.tick(1) // swap
		j = p
	}
}

func (h *Heap[T]) down(i, n int) {
	for {
		l := 2*i + 1
		if l >= n {
			return
		}
		s := l
		if r := l + 1; r < n {
			h.tick(1) // compare the two children
			if h.less(h.data[r], h.data[l]) {
				s = r
			}
		}
		h.tick(1) // compare selected child with parent
		if !h.less(h.data[s], h.data[i]) {
			return
		}
		h.data[i], h.data[s] = h.data[s], h.data[i]
		h.tick(1) // swap
		i = s
	}
}
