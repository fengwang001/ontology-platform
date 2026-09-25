// Package rank selects the top-k candidates by (frequency desc, string asc)
// using a bounded min-heap.
package rank

import (
	"container/heap"
	"sync/atomic"

	"ontology/trie"
)

// collected counts candidates examined by TopK (test-only counter).
var collected atomic.Int64

type cand struct {
	s string
	f int
}

// better reports whether a ranks before b: frequency desc, then string asc.
func better(a, b cand) bool {
	if a.f != b.f {
		return a.f > b.f
	}
	return a.s < b.s
}

// minHeap keeps the worst of the selected candidates at the top.
type minHeap []cand

func (h minHeap) Len() int           { return len(h) }
func (h minHeap) Less(i, j int) bool { return better(h[j], h[i]) }
func (h minHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)        { *h = append(*h, x.(cand)) }
func (h *minHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// TopK returns the k highest-ranked strings under prefix, in rank order.
func TopK(t *trie.Trie, prefix string, k int) []string {
	h := &minHeap{}
	heap.Init(h)
	t.Walk(prefix, func(s string, f int) {
		collected.Add(1)
		c := cand{s, f}
		if h.Len() < k {
			heap.Push(h, c)
		} else if better(c, (*h)[0]) {
			(*h)[0] = c
			heap.Fix(h, 0)
		}
	})
	out := make([]string, h.Len())
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = heap.Pop(h).(cand).s
	}
	return out
}
