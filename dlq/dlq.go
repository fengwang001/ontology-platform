// Package dlq is the core of the delayed task queue: a min-heap ordered by
// (fireAt, registration sequence), lazy tombstones for cancellation, sequence
// allocation, and due-pop ordering. It depends on no other package.
package dlq

import (
	"math/bits"
	"strconv"
)

// Handle is an opaque reference to one scheduled heap entry. Cancelling via
// the entry's own handle flips that generation's tombstone, so rescheduling
// the same id later is an independent entry.
type Handle struct{ n *node }

// Seq returns the entry's global registration sequence.
func (hd *Handle) Seq() uint64 { return hd.n.seq }

// FireAt returns the entry's absolute fire time.
func (hd *Handle) FireAt() int64 { return hd.n.fireAt }

// Cancel marks this entry as a cancelled tombstone.
func (hd *Handle) Cancel() { hd.n.cancelled = true }

type node struct {
	id        string
	fireAt    int64
	seq       uint64
	cancelled bool
}

// Heap is the delayed min-heap. The zero value is NOT ready; use New.
type Heap struct {
	items     []*node
	nextSeq   uint64
	inspected int // nodes examined during the most recent PopDue (unexported)
}

// New returns an empty heap.
func New() *Heap { return &Heap{} }

// Len reports the number of entries still physically in the heap, including
// cancelled tombstones not yet popped.
func (h *Heap) Len() int { return len(h.items) }

func (h *Heap) less(a, b *node) bool {
	if a.fireAt != b.fireAt {
		return a.fireAt < b.fireAt
	}
	return a.seq < b.seq
}

// Push registers an entry with a fresh sequence and returns its handle.
func (h *Heap) Push(id string, fireAt int64) *Handle {
	h.nextSeq++
	n := &node{id: id, fireAt: fireAt, seq: h.nextSeq}
	h.items = append(h.items, n)
	i := len(h.items) - 1
	for i > 0 { // sift up
		p := (i - 1) / 2
		if !h.less(h.items[i], h.items[p]) {
			break
		}
		h.items[i], h.items[p] = h.items[p], h.items[i]
		i = p
	}
	return &Handle{n: n}
}

// popRoot removes and returns the minimum; every node visited on the sift-
// down path is counted in inspected.
func (h *Heap) popRoot() *node {
	root := h.items[0]
	last := len(h.items) - 1
	h.items[0] = h.items[last]
	h.items = h.items[:last]
	i := 0
	for {
		l := 2*i + 1
		if l >= len(h.items) {
			break
		}
		h.inspected++ // left child examined
		c := l
		if r := l + 1; r < len(h.items) {
			h.inspected++ // right child examined
			if h.less(h.items[r], h.items[l]) {
				c = r
			}
		}
		if !h.less(h.items[c], h.items[i]) {
			break
		}
		h.items[i], h.items[c] = h.items[c], h.items[i]
		i = c
	}
	return root
}

// PopDue pops every entry with fireAt <= now in (fireAt, seq) order, skipping
// cancelled tombstones, and stops as soon as the heap minimum is in the
// future. It resets the inspected counter on entry.
func (h *Heap) PopDue(now int64) []string {
	h.inspected = 0
	fired := []string{}
	for len(h.items) > 0 {
		h.inspected++ // root examined for the due test
		if h.items[0].fireAt > now {
			break
		}
		n := h.popRoot()
		if !n.cancelled {
			fired = append(fired, n.id)
		}
	}
	return fired
}

// PopCostBounded reports whether popping one due entry out of a heap holding
// m far-future entries examines only O(log m) nodes, across several m. It
// exposes a boolean verdict only; the raw counter value stays unexported.
func PopCostBounded() bool {
	for _, m := range []int{100, 1000, 10000} {
		h := New()
		for i := 0; i < m; i++ {
			h.Push("f"+strconv.Itoa(i), int64(1000+i))
		}
		h.Push("early", 1)
		h.PopDue(1)
		if h.inspected > 4*bits.Len(uint(m)) {
			return false
		}
	}
	return true
}
