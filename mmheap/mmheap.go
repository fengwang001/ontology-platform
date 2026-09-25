// Package mmheap is a min-max heap: levels alternate min/max, so the root
// is the min and the larger root child the max. Not concurrency-safe.
package mmheap

import (
	"errors"
	"math/bits"
)

// ErrCorrupt is returned by Check when a level invariant is violated.
var ErrCorrupt = errors.New("mmheap: level invariant violated")

// Heap is a min-max heap of int64; last counts nodes checked/swapped by
// the latest Push/Delete* and is readable only through ScaleOK.
type Heap struct {
	a    []int64
	last int
}

func level(i int) int    { return bits.Len(uint(i+1)) - 1 } // depth of i; even = min level
func (h *Heap) Len() int { return len(h.a) }
func (h *Heap) root(i int) (int64, bool) { // a[i], or ok=false when empty
	if len(h.a) == 0 {
		return 0, false
	}
	return h.a[i], true
}
func (h *Heap) Min() (int64, bool) { return h.root(0) }

// Max returns the larger root child (or root); touches no counter.
func (h *Heap) Max() (int64, bool) { return h.root(h.maxRoot(false)) }
func (h *Heap) maxRoot(count bool) int { // index of max among a[:3]
	i := 0
	for j := 1; j < len(h.a) && j < 3; j++ {
		if count {
			h.last++
		}
		if h.a[j] > h.a[i] {
			i = j
		}
	}
	return i
}
func (h *Heap) Push(v int64) {
	h.last = 0
	h.a = append(h.a, v)
	i := len(h.a) - 1 // for i==0, p==0==i and the comparison is false
	p := (i - 1) / 2
	h.last++
	if min := level(i)%2 == 0; (h.a[i] > h.a[p]) == min {
		h.swap(i, p) // crosses the parent level's order
		h.up(p, !min)
	} else {
		h.up(i, min)
	}
}
func (h *Heap) up(i int, min bool) { // bubble a[i] up same-parity levels
	for i > 2 {
		g := (i - 3) / 4
		h.last++
		if (h.a[i] < h.a[g]) != min {
			return
		}
		h.swap(i, g)
		i = g
	}
}
func (h *Heap) DeleteMin() (int64, bool) { return h.del(false) }
func (h *Heap) DeleteMax() (int64, bool) { return h.del(true) }
func (h *Heap) del(max bool) (int64, bool) { // remove root, or max of a[:3]
	h.last = 0
	if len(h.a) == 0 {
		return 0, false
	}
	i := 0
	if max {
		i = h.maxRoot(true)
	}
	v, n := h.a[i], len(h.a)-1
	h.a[i], h.a = h.a[n], h.a[:n]
	if i < n {
		h.down(i, level(i)%2 == 0)
	}
	return v, true
}
func (h *Heap) down(i int, min bool) { // trickle a[i] down its parity
	for 2*i+1 < len(h.a) {
		m, grand := h.extreme(i, min)
		h.last++
		if (h.a[m] < h.a[i]) != min {
			return
		}
		h.swap(i, m)
		if !grand {
			return
		}
		p := (m - 1) / 2
		h.last++
		if (h.a[m] > h.a[p]) == min {
			h.swap(m, p)
		}
		i = m
	}
}

// extreme returns the min/max among children and grandchildren of i,
// and whether that index is a grandchild.
func (h *Heap) extreme(i int, min bool) (m int, grand bool) {
	m = -1
	better := func(j int) bool {
		h.last++
		return m < 0 || min && h.a[j] < h.a[m] || !min && h.a[j] > h.a[m]
	}
	for c := 2*i + 1; c < len(h.a) && c <= 2*i+2; c++ {
		if better(c) {
			m, grand = c, false
		}
		for g := 2*c + 1; g < len(h.a) && g <= 2*c+2; g++ {
			if better(g) {
				m, grand = g, true
			}
		}
	}
	return
}
func (h *Heap) swap(i, j int) {
	h.a[i], h.a[j] = h.a[j], h.a[i]
	h.last++
}

// Check verifies each node against its parent and grandparent levels
// (transitivity then implies the whole-subtree invariant).
func (h *Heap) Check() error {
	for i := 1; i < len(h.a); i++ {
		for _, j := range [2]int{(i - 1) / 2, (i - 3) / 4} {
			if min := level(j)%2 == 0; min && h.a[i] < h.a[j] || !min && h.a[i] > h.a[j] {
				return ErrCorrupt
			}
		}
	}
	return nil
}

// ScaleOK reports whether the last mutating op touched at most a small
// constant times log2(m) nodes; it never exposes the raw counter.
func (h *Heap) ScaleOK(m int) bool { return h.last <= 16*bits.Len(uint(m+1)) }
