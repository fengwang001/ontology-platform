// Package tier tracks per-key access timestamps and selects the LRU
// eviction candidate. It depends on no other package in the module.
package tier

import "container/heap"

// Entry is a hot-tier key tagged with its most recent access timestamp.
type Entry struct {
	Key string
	At  int64
	idx int // heap index, maintained by the heap methods
}

// Clock hands out strictly increasing access timestamps, starting at 1.
type Clock struct {
	next int64
}

func NewClock() *Clock { return &Clock{} }

// Tick returns the next monotonically increasing timestamp.
func (c *Clock) Tick() int64 {
	c.next++
	return c.next
}

// candidates is a min-heap ordered by (At, Key): smallest timestamp is
// the LRU victim; ties break to the lexicographically smaller key.
type candidates []*Entry

func (h candidates) Len() int { return len(h) }
func (h candidates) Less(i, j int) bool {
	if h[i].At != h[j].At {
		return h[i].At < h[j].At
	}
	return h[i].Key < h[j].Key
}
func (h candidates) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].idx = i
	h[j].idx = j
}
func (h *candidates) Push(x any) {
	e := x.(*Entry)
	e.idx = len(*h)
	*h = append(*h, e)
}
func (h *candidates) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	e.idx = -1
	return e
}

// Heap wraps the ordered structure so callers never scan the hot tier:
// selecting a victim inspects the root alone, in O(1).
type Heap struct{ h candidates }

func NewHeap() *Heap { return &Heap{} }

func (h *Heap) Add(e *Entry)             { heap.Push(&h.h, e) }
func (h *Heap) Touch(e *Entry, at int64) { e.At = at; heap.Fix(&h.h, e.idx) }
func (h *Heap) Pop() *Entry              { return heap.Pop(&h.h).(*Entry) }
func (h *Heap) Len() int                 { return h.h.Len() }
