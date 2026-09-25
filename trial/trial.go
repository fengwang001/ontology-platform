// Package trial implements trial-deletion cycle collection on top of refc.
// It depends only on refc; the dependency direction never reverses.
package trial

import (
	"errors"

	"ontology/refc"
)

// Sentinel errors; all rejections are decided with errors.Is and the three
// failure kinds in the system are pairwise distinct.
var (
	// ErrInvalidObject: the argument is unknown or has already been freed.
	ErrInvalidObject = errors.New("refc: unknown or released object")
	// ErrNoRootToDrop: Unroot would drive rc below zero.
	ErrNoRootToDrop = errors.New("refc: object has no root reference to drop")
)

// Collector runs trial deletion and the guarded mutators. Every method assumes
// the caller holds the heap lock (the api package is the only caller).
type Collector struct {
	h *refc.Heap
	// lastPointChecks counts object records examined by the most recent Point.
	// Unexported on purpose: no public method exposes it; white-box tests in
	// this package read the field directly.
	lastPointChecks int
}

func NewCollector(h *refc.Heap) *Collector { return &Collector{h: h} }

// Point repoints from.child to to after validating both operands. Validation
// happens before any mutation, so a rejected Point leaves no trace. The check
// count is local: it only looks at from/to (and the old child via Repoint),
// never scans the heap, so it is independent of the number of live objects.
func (c *Collector) Point(from, to refc.Obj) error {
	nf, ok := c.h.Get(from)
	if !ok {
		c.lastPointChecks = 1 // only the from record was examined
		return ErrInvalidObject
	}
	if to != 0 {
		if _, ok := c.h.Get(to); !ok {
			c.lastPointChecks = 2
			return ErrInvalidObject
		}
	}
	c.lastPointChecks = 1
	if to != 0 {
		c.lastPointChecks = 2
	}
	c.h.Repoint(nf, to)
	return nil
}

// Unroot removes one root reference from o. It is refused when o carries no
// root reference (Roots==0): subtracting one then would drive the root
// component negative. Validation precedes any mutation.
func (c *Collector) Unroot(o refc.Obj) error {
	n, ok := c.h.Get(o)
	if !ok {
		return ErrInvalidObject
	}
	if n.Roots == 0 {
		return ErrNoRootToDrop
	}
	c.h.DropRoot(o)
	return nil
}

// Collect performs one deterministic trial-deletion pass and returns the
// number of records freed. Steps: (1) tmp=rc; (2) subtract every field
// reference, so tmp[o] is the number of direct root references; (3) rescue
// every tmp>0 object together with its child-reachability closure; (4) sweep
// the rest. Edges from swept garbage into survivors are dropped as ordinary
// reference removals so survivor rc stays equal to true indegree.
func (c *Collector) Collect() int {
	live := c.h.Nodes()
	tmp := make(map[refc.Obj]int, len(live))
	for id, n := range live {
		tmp[id] = n.RC
	}
	for _, n := range live {
		if n.Child != 0 {
			tmp[n.Child]--
		}
	}
	alive := make(map[refc.Obj]bool, len(live))
	var stack []refc.Obj
	for id, t := range tmp {
		if t > 0 {
			alive[id] = true
			stack = append(stack, id)
		}
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if ch := live[cur].Child; ch != 0 && !alive[ch] {
			alive[ch] = true
			stack = append(stack, ch)
		}
	}
	freed := 0
	ids := make([]refc.Obj, 0, len(live))
	for id := range live {
		ids = append(ids, id)
	}
	for _, id := range ids {
		if alive[id] {
			continue
		}
		ch := live[id].Child
		c.h.Free(id) // Free clears no edge; record itself is gone
		freed++
		if ch != 0 && alive[ch] {
			c.h.DecRelease(ch) // drop garbage->survivor edge; survivor stays rc>=1
		}
	}
	return freed
}
