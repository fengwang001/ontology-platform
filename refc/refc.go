// Package refc stores the object graph: each object has one child pointer and
// a reference count, and owns cascade release. It depends on no other package.
package refc

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Obj identifies a heap object. The zero value denotes the nil pointer.
type Obj uint64

// Node is a live object record; all access happens with the Heap lock held.
type Node struct {
	ID    Obj
	RC    int // total references, always >= 1 for a record in the Heap
	Roots int // direct root references; RC == Roots + live field indegree
	Child Obj // outgoing field reference; 0 means nil
}

// Heap is the process-memory object store. One mutex serializes every
// operation, including a whole trial-deletion Collect cycle.
type Heap struct {
	mu    sync.Mutex
	next  Obj
	nodes map[Obj]*Node
}

func New() *Heap { return &Heap{nodes: map[Obj]*Node{}} }

func (h *Heap) Lock()   { h.mu.Lock() }
func (h *Heap) Unlock() { h.mu.Unlock() }
func (h *Heap) Len() int {
	return len(h.nodes)
}
func (h *Heap) Get(o Obj) (*Node, bool) { n, ok := h.nodes[o]; return n, ok }
func (h *Heap) Free(o Obj)              { delete(h.nodes, o) }
func (h *Heap) Nodes() map[Obj]*Node    { return h.nodes }

// Root allocates an object with rc=1, roots=1, child=nil. Caller holds lock.
func (h *Heap) Root() *Node {
	h.next++
	n := &Node{ID: h.next, RC: 1, Roots: 1}
	h.nodes[n.ID] = n
	return n
}

// Repoint moves n's child to t (t==0 clears it): RC[t]++ first, switch the
// pointer, then drop one reference from the old child (may cascade-free).
// Caller validated n and t (0 or live) and holds the lock.
func (h *Heap) Repoint(n *Node, t Obj) int {
	old := n.Child
	if t != 0 {
		h.nodes[t].RC++
	}
	n.Child = t
	if old != 0 {
		return h.DecRelease(old)
	}
	return 0
}

// DropRoot removes one direct root reference (Roots--, RC--), cascading on
// zero. The caller (trial.Unroot) already guaranteed Roots > 0.
func (h *Heap) DropRoot(o Obj) int {
	h.nodes[o].Roots--
	return h.DecRelease(o)
}

// DecRelease removes one incoming reference: RC--. At zero it frees o, clears
// its child and cascades the decrement onward. A missing child stops the walk,
// so a cycle cannot loop. It returns how many records were freed. Lock held.
func (h *Heap) DecRelease(o Obj) int {
	freed := 0
	for o != 0 {
		n, ok := h.nodes[o]
		if !ok {
			return freed
		}
		n.RC--
		if n.RC > 0 {
			return freed
		}
		child := n.Child
		n.Child = 0 // no pointer outlives the record it belongs to
		delete(h.nodes, o)
		freed++
		o = child
	}
	return freed
}

// Diagnose verifies conservation (RC == Roots + live field indegree, RC>=1)
// and the absence of a dangling child pointer over every live record.
func (h *Heap) Diagnose() error {
	in := map[Obj]int{}
	for _, n := range h.nodes {
		if n.Child != 0 {
			in[n.Child]++
		}
	}
	for id, n := range h.nodes {
		switch {
		case n.RC < 1:
			return fmt.Errorf("refc: rc[%d]=%d < 1", id, n.RC)
		case n.RC != n.Roots+in[id]:
			return fmt.Errorf("refc: rc[%d]=%d != roots %d + indeg %d", id, n.RC, n.Roots, in[id])
		case n.Child != 0 && h.nodes[n.Child] == nil:
			return fmt.Errorf("refc: dangling child %d->%d", id, n.Child)
		}
	}
	return nil
}

// Orphans counts live records unreachable from directly rooted objects. A
// correct Collect must leave zero (unreachable cycles are exactly its target).
func (h *Heap) Orphans() int {
	seen, st := map[Obj]bool{}, []Obj{}
	for id, n := range h.nodes {
		if n.Roots > 0 {
			seen[id], st = true, append(st, id)
		}
	}
	for len(st) > 0 {
		x := st[len(st)-1]
		st = st[:len(st)-1]
		if y := h.nodes[x].Child; y != 0 && !seen[y] {
			seen[y], st = true, append(st, y)
		}
	}
	return len(h.nodes) - len(seen)
}

// Fingerprint deterministically serializes every live record; compare before
// and after a rejected operation to prove it left no trace.
func (h *Heap) Fingerprint() string {
	ids := make([]Obj, 0, len(h.nodes))
	for id := range h.nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var b strings.Builder
	for _, id := range ids {
		n := h.nodes[id]
		fmt.Fprintf(&b, "%d:%d:%d:%d;", id, n.RC, n.Roots, n.Child)
	}
	return b.String()
}
