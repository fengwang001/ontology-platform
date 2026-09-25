// Package refc holds the raw reference-counted object graph: per-object root
// and field counts, one child edge, and cascading release. No trial deletion.
package refc

import "sort"

// Obj is an object identifier. Zero means nil.
type Obj int64

type rec struct {
	roots, fields int
	child         Obj
}

// Heap is the process-resident graph. Not safe for concurrent use.
type Heap struct {
	next  Obj
	recs  map[Obj]*rec
	limit int
}

// NewHeap creates an empty heap with the given live-object limit.
func NewHeap(limit int) *Heap {
	return &Heap{next: 1, recs: make(map[Obj]*rec), limit: limit}
}

// Snap is an immutable snapshot of one live record.
type Snap struct {
	O             Obj
	Roots, Fields int
	Child         Obj
}

func (h *Heap) Len() int { return len(h.recs) }

// Root allocates an object with roots==1; false at the limit.
func (h *Heap) Root() (Obj, bool) {
	if len(h.recs) >= h.limit {
		return 0, false
	}
	o := h.next
	h.next++
	h.recs[o] = &rec{roots: 1}
	return o, true
}

func (h *Heap) Alive(o Obj) bool { _, ok := h.recs[o]; return ok }
func (h *Heap) Roots(o Obj) int {
	if r := h.recs[o]; r != nil {
		return r.roots
	}
	return 0
}
func (h *Heap) RC(o Obj) int {
	if r := h.recs[o]; r != nil {
		return r.roots + r.fields
	}
	return 0
}
func (h *Heap) Child(o Obj) Obj {
	if r := h.recs[o]; r != nil {
		return r.child
	}
	return 0
}

// releaseFromField deletes first (rc just hit zero) and cascades along child.
func (h *Heap) releaseFromField(first Obj) {
	stack := []Obj{first}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		cr := h.recs[x]
		if cr == nil {
			continue // released earlier through another dead parent
		}
		cr.fields--
		if cr.roots+cr.fields > 0 {
			continue
		}
		c := cr.child
		delete(h.recs, x)
		if c != 0 {
			stack = append(stack, c)
		}
	}
}

// Unroot drops one root from live o and cascade-releases at rc zero.
// Precondition: o is alive with roots > 0.
func (h *Heap) Unroot(o Obj) {
	r := h.recs[o]
	r.roots--
	if r.roots+r.fields > 0 {
		return
	}
	c := r.child
	delete(h.recs, o)
	if c != 0 {
		h.releaseFromField(c)
	}
}

// Point rewires from.child to to (0 clears it). The new edge is added before
// the old one is dropped, so `to` can never be collected by the old cascade.
// Precondition: from alive; to is 0 or alive.
func (h *Heap) Point(from, to Obj) {
	r := h.recs[from]
	if r.child == to {
		return
	}
	old := r.child
	if to != 0 {
		h.recs[to].fields++
	}
	r.child = to
	if old != 0 {
		h.releaseFromField(old)
	}
}

// Snapshot returns live records ordered by object id.
func (h *Heap) Snapshot() []Snap {
	out := make([]Snap, 0, len(h.recs))
	for o, r := range h.recs {
		out = append(out, Snap{O: o, Roots: r.roots, Fields: r.fields, Child: r.child})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].O < out[j].O })
	return out
}

// Sweep deletes every object in free and decrements field counts of surviving
// children across the boundary. Precondition: free is exactly the unreachable
// set, so every survivor keeps rc > 0 afterwards.
func (h *Heap) Sweep(free []Obj) {
	inFree := make(map[Obj]bool, len(free))
	for _, x := range free {
		inFree[x] = true
	}
	for _, x := range free { // boundary edges first, while all records exist
		if r := h.recs[x]; r != nil && r.child != 0 && !inFree[r.child] {
			if cr := h.recs[r.child]; cr != nil {
				cr.fields--
			}
		}
	}
	for _, x := range free {
		delete(h.recs, x)
	}
}
