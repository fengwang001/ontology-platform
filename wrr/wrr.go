// Package wrr implements the smooth weighted round-robin (nginx-style) core.
package wrr

import (
	"container/heap"
	"fmt"
	"sync"
)

// maxHeap orders server indices by key cw[i]+w[i], largest first; ties go to
// the smaller index. Less also counts the comparisons spent inside Next.
type maxHeap struct {
	b   *WRR
	idx []int
}

func (h *maxHeap) Len() int { return len(h.idx) }

func (h *maxHeap) Less(i, j int) bool {
	h.b.lastChecks++
	a, c := h.idx[i], h.idx[j]
	ka, kc := h.b.cw[a]+h.b.w[a], h.b.cw[c]+h.b.w[c]
	if ka != kc {
		return ka > kc
	}
	return a < c
}

func (h *maxHeap) Swap(i, j int) { h.idx[i], h.idx[j] = h.idx[j], h.idx[i] }

func (h *maxHeap) Push(x any) { h.idx = append(h.idx, x.(int)) }

func (h *maxHeap) Pop() any {
	old := h.idx
	x := old[len(old)-1]
	h.idx = old[:len(old)-1]
	return x
}

// WRR is the smooth weighted round-robin core. Safe for concurrent use.
type WRR struct {
	mu         sync.Mutex
	w          []int // server weights, all >= 1
	cw         []int // current weights, always summing to 0
	total      int   // W = sum(w)
	h          maxHeap
	dirty      bool // heap must be revalidated before the next selection
	lastChecks int  // comparisons spent locating the max in the latest Next
}

// New builds the core. Callers (svc) must guarantee weights is non-empty
// with every weight >= 1.
func New(weights []int) *WRR {
	b := &WRR{w: append([]int(nil), weights...), cw: make([]int, len(weights))}
	for _, x := range weights {
		b.total += x
	}
	b.h.b = b
	b.h.idx = make([]int, len(weights))
	for i := range b.h.idx {
		b.h.idx[i] = i
	}
	heap.Init(&b.h)
	b.lastChecks = 0
	return b
}

// Next runs one round: add w_i to every cw_i, pick the largest (ties go to
// the smaller index), subtract W from the picked server, return its index.
func (b *WRR) Next() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastChecks = 0
	if b.dirty {
		heap.Init(&b.h)
		b.dirty = false
	}
	r := b.h.idx[0]
	b.lastChecks++ // on a valid heap, locating the max inspects only the root
	for i := range b.cw {
		b.cw[i] += b.w[i]
	}
	b.cw[r] -= b.total
	b.dirty = true
	return r
}

// SetWeight updates w[i], recomputes W and resets every cw to 0.
func (b *WRR) SetWeight(i, w int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.w[i] = w
	b.total = 0
	for j, x := range b.w {
		b.total += x
		b.cw[j] = 0
	}
	heap.Init(&b.h)
	b.dirty = false
	b.lastChecks = 0
}

// Weight returns w[i], or 0 when i is out of range.
func (b *WRR) Weight(i int) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if i < 0 || i >= len(b.w) {
		return 0
	}
	return b.w[i]
}

// Len returns the server count.
func (b *WRR) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.w)
}

// SumCW returns the sum of all current weights (invariant: always 0).
func (b *WRR) SumCW() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := 0
	for _, x := range b.cw {
		s += x
	}
	return s
}

// CheckScalability verifies that locating the max in one Next costs a number
// of comparisons bounded by a small constant independent of m (heap root,
// not a full scan). It reports only a verdict, never the counter value.
func CheckScalability() error {
	const bound = 64
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		w := make([]int, m)
		for i := range w {
			w[i] = i + 1 // distinct weights
		}
		b := New(w)
		b.Next()
		if b.lastChecks > bound {
			return fmt.Errorf("wrr: m=%d needed %d checks (> %d)", m, b.lastChecks, bound)
		}
	}
	return nil
}
