// Package trial implements trial-deletion cycle collection on top of a
// refc.Heap, the sentinel errors for rejected operations, and the internal
// complexity counter for Point.
package trial

import (
	"ontology/refc"
)

// Sentinel errors for the three distinguishable failure modes.
var (
	// ErrUnknownObject: Point/Unroot on an unknown or already freed object.
	ErrUnknownObject = errUnknown{}
	// ErrNoRoot: Unroot would drive an object's root count below zero.
	ErrNoRoot = errNoRoot{}
	// ErrLimit: Root would exceed the configured object limit.
	ErrLimit = errLimit{}
)

type errUnknown struct{}

func (errUnknown) Error() string { return "unknown or released object" }

type errNoRoot struct{}

func (errNoRoot) Error() string { return "object has no root reference to remove" }

type errLimit struct{}

func (errLimit) Error() string { return "object limit exceeded" }

// Graph wraps a refc heap with validated, error-returning operations and
// trial-deletion collection. Not safe for concurrent use.
type Graph struct {
	h *refc.Heap
	// pointChecks counts object records examined by the most recent Point
	// rewire: at most the three records from, old child and new to. It is
	// intentionally unexported and has no exported accessor.
	pointChecks int
}

// New creates a Graph with the given live-object limit.
func New(limit int) *Graph { return &Graph{h: refc.NewHeap(limit)} }

// Heap exposes the underlying heap for read-only introspection.
func (g *Graph) Heap() *refc.Heap { return g.h }

// Root allocates a rooted object.
func (g *Graph) Root() (refc.Obj, error) {
	o, ok := g.h.Root()
	if !ok {
		return 0, ErrLimit
	}
	return o, nil
}

// Unroot removes one root from o. All checks precede the mutation, so a
// rejected call changes no state.
func (g *Graph) Unroot(o refc.Obj) error {
	if !g.h.Alive(o) {
		return ErrUnknownObject
	}
	if g.h.Roots(o) <= 0 {
		return ErrNoRoot
	}
	g.h.Unroot(o)
	return nil
}

// Point rewires from.child to to (0 clears it). It examines at most three
// records (from, the old child, and to) regardless of heap size.
func (g *Graph) Point(from, to refc.Obj) error {
	if !g.h.Alive(from) {
		return ErrUnknownObject
	}
	if to != 0 && !g.h.Alive(to) {
		return ErrUnknownObject
	}
	old := g.h.Child(from)
	seen := map[refc.Obj]bool{from: true}
	if old != 0 {
		seen[old] = true
	}
	if to != 0 {
		seen[to] = true
	}
	g.pointChecks = len(seen)
	g.h.Point(from, to)
	return nil
}

// Collect reclaims every root-unreachable object and returns the count:
// tmp starts at rc, loses one per incoming field edge, objects with tmp>0
// seed a rescue traversal along child edges, and everything unmarked is
// garbage (cycles or dead subgraphs).
func (g *Graph) Collect() int {
	snaps := g.h.Snapshot()
	tmp := make(map[refc.Obj]int, len(snaps))
	child := make(map[refc.Obj]refc.Obj, len(snaps))
	for _, s := range snaps {
		tmp[s.O] = s.Roots + s.Fields
		child[s.O] = s.Child
	}
	for _, s := range snaps { // step 2: drop all field references
		if c := s.Child; c != 0 {
			if _, live := tmp[c]; live {
				tmp[c]--
			}
		}
	}
	keep := make(map[refc.Obj]bool, len(snaps)) // step 3: rescue
	var stack []refc.Obj
	for _, s := range snaps {
		if tmp[s.O] > 0 {
			keep[s.O] = true
			stack = append(stack, s.O)
		}
	}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if c := child[x]; c != 0 && !keep[c] {
			if _, live := tmp[c]; live {
				keep[c] = true
				stack = append(stack, c)
			}
		}
	}
	var free []refc.Obj // step 4: sweep the unmarked
	for _, s := range snaps {
		if !keep[s.O] {
			free = append(free, s.O)
		}
	}
	g.h.Sweep(free)
	return len(free)
}
