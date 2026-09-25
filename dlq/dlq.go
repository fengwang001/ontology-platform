// Package dlq is the core of the delayed task queue: a min-heap ordered by
// (fireAt, registration sequence) with lazy per-entry cancellation. It has no
// dependency on the other packages.
package dlq

import (
	"container/heap"
	"math"
	"strconv"
)

// Entry is one scheduled task. Canceled is a lazy tombstone: the entry stays
// in the heap and is skipped when it reaches the top.
type Entry struct {
	ID       string
	FireAt   int64
	Seq      int64
	canceled bool
}

// Heap is the delayed min-heap. The zero value is not ready; use New.
type Heap struct {
	items   []*Entry
	nextSeq int64
	// checks counts heap nodes inspected during the most recent PopDue call.
	// It is deliberately unexported: callers must never read the number.
	checks int
}

// New returns an empty heap.
func New() *Heap { return &Heap{} }

// Schedule allocates the next global registration sequence and inserts the
// entry, then returns it so the caller can cancel exactly this generation.
func (h *Heap) Schedule(id string, fireAt int64) *Entry {
	h.nextSeq++
	e := &Entry{ID: id, FireAt: fireAt, Seq: h.nextSeq}
	heap.Push((*entryHeap)(h), e)
	return e
}

// Cancel marks a single entry (one generation of an id) as canceled.
func (h *Heap) Cancel(e *Entry) { e.canceled = true }

// PopDue removes and returns every active entry with FireAt <= now, in
// (fireAt, seq) order. Canceled entries that reach the top are dropped.
func (h *Heap) PopDue(now int64) []*Entry {
	h.checks = 0
	var fired []*Entry
	p := (*entryHeap)(h)
	for p.Len() > 0 {
		h.checks++ // inspect the current root
		root := p.items[0]
		if root.FireAt > now {
			break
		}
		heap.Pop(p)
		if !root.canceled {
			fired = append(fired, root)
		}
	}
	return fired
}

// Peek returns the active entry that would fire first, dropping canceled
// entries sitting on top. Nothing due is removed.
func (h *Heap) Peek() (*Entry, bool) {
	p := (*entryHeap)(h)
	for p.Len() > 0 && p.items[0].canceled {
		heap.Pop(p)
	}
	if p.Len() == 0 {
		return nil, false
	}
	return p.items[0], true
}

// entryHeap gives Heap the container/heap interface.
type entryHeap Heap

func (h *entryHeap) Len() int { return len(h.items) }

func (h *entryHeap) Less(i, j int) bool {
	a, b := h.items[i], h.items[j]
	if a.FireAt != b.FireAt {
		return a.FireAt < b.FireAt
	}
	return a.Seq < b.Seq
}

func (h *entryHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *entryHeap) Push(x any) { h.items = append(h.items, x.(*Entry)) }

func (h *entryHeap) Pop() any {
	n := len(h.items)
	e := h.items[n-1]
	h.items[n-1] = nil
	h.items = h.items[:n-1]
	return e
}

// PopCheckSublinear verifies without exposing the counter that firing one due
// task among m far-future tasks inspects a number of nodes bounded by a
// constant times log m rather than growing with m.
func PopCheckSublinear() bool {
	for _, m := range []int{100, 1000, 10000} {
		h := New()
		for i := 0; i < m; i++ {
			h.Schedule("x"+strconv.Itoa(i), 1_000_000)
		}
		h.Schedule("early", 0)
		fired := h.PopDue(0)
		if len(fired) != 1 || fired[0].ID != "early" {
			return false
		}
		bound := 4 * math.Ceil(math.Log2(float64(m+1)))
		if float64(h.checks) > bound {
			return false
		}
	}
	return true
}
