// Package plan detects rename conflicts and computes a safe execution
// order via topological sort; remaining cycles are left to package cycle.
package plan

import (
	"container/heap"
	"errors"
	"fmt"
	"sort"

	"ontology/name"
)

// Request asks to rename From to To.
type Request struct{ From, To string }

// Step is one concrete rename to execute.
type Step struct{ From, To string }

var (
	ErrDuplicateSource = errors.New("plan: duplicate source name")
	ErrDuplicateTarget = errors.New("plan: duplicate target name")
	ErrMissingSource   = errors.New("plan: source name not in namespace")
	ErrTargetExists    = errors.New("plan: target exists and is not moved away")
	ErrInvalidName     = errors.New("plan: invalid name")
)

// ConflictError describes a rejected batch; Unwrap yields one of the
// sentinel errors above so errors.Is can classify it.
type ConflictError struct {
	Err        error
	From, To   string // offending request
	To2, From2 string // the other new/old name for duplicates
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%v: %q -> %q", e.Err, e.From, e.To)
}
func (e *ConflictError) Unwrap() error { return e.Err }

// lookups counts every map/set lookup; tests assert it stays linear.
var lookups int64

// LookupCount exposes the internal counter for the demo.
func LookupCount() int64 { return lookups }

func resetLookups() { lookups = 0 }

// Check validates the batch against ns without modifying anything.
// Self-loops (a->a) are no-ops and dropped before any check.
func Check(ns *name.Set, reqs []Request) error {
	froms := make(map[string]Request, len(reqs))
	tos := make(map[string]Request, len(reqs))
	for _, r := range reqs {
		if r.From == r.To {
			continue
		}
		if !name.Valid(r.From) || !name.Valid(r.To) {
			return &ConflictError{Err: ErrInvalidName, From: r.From, To: r.To}
		}
		lookups++
		if prev, dup := froms[r.From]; dup {
			return &ConflictError{Err: ErrDuplicateSource, From: r.From, To: prev.To, To2: r.To}
		}
		lookups++
		if prev, dup := tos[r.To]; dup {
			return &ConflictError{Err: ErrDuplicateTarget, From: prev.From, From2: r.From, To: r.To}
		}
		froms[r.From] = r
		tos[r.To] = r
	}
	for _, r := range reqs {
		if r.From == r.To {
			continue
		}
		lookups++
		if !ns.Contains(r.From) {
			return &ConflictError{Err: ErrMissingSource, From: r.From, To: r.To}
		}
		lookups++
		if _, moved := froms[r.To]; !moved {
			lookups++
			if ns.Contains(r.To) {
				return &ConflictError{Err: ErrTargetExists, From: r.From, To: r.To}
			}
		}
	}
	return nil
}

// Order topologically sorts the batch: R runs after Q when R.To == Q.From.
// Ready nodes pop in lexicographic From order for determinism. Leftover
// nodes form disjoint simple cycles, returned in cycle-start order.
func Order(reqs []Request) (steps []Step, cycles [][]Request) {
	froms := make(map[string]Request, len(reqs))
	tos := make(map[string]Request, len(reqs))
	for _, r := range reqs {
		if r.From != r.To {
			froms[r.From] = r
			tos[r.To] = r
		}
	}
	ready := &reqHeap{}
	for _, r := range froms {
		lookups++
		if _, dep := froms[r.To]; !dep {
			heap.Push(ready, r)
		}
	}
	for ready.Len() > 0 {
		r := heap.Pop(ready).(Request)
		steps = append(steps, Step{From: r.From, To: r.To})
		delete(froms, r.From)
		lookups++
		if q, ok := tos[r.From]; ok {
			lookups++
			if _, pending := froms[q.From]; pending {
				heap.Push(ready, q)
			}
		}
	}
	keys := make([]string, 0, len(froms))
	for k := range froms {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	seen := make(map[string]bool, len(froms))
	for _, k := range keys {
		if seen[k] {
			continue
		}
		var cyc []Request
		for cur := froms[k]; !seen[cur.From]; {
			seen[cur.From] = true
			cyc = append(cyc, cur)
			lookups++
			next, ok := froms[cur.To]
			if !ok {
				break
			}
			cur = next
		}
		cycles = append(cycles, cyc)
	}
	return steps, cycles
}

// reqHeap is a min-heap of requests ordered by From.
type reqHeap []Request

func (h reqHeap) Len() int           { return len(h) }
func (h reqHeap) Less(i, j int) bool { return h[i].From < h[j].From }
func (h reqHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *reqHeap) Push(x any)        { *h = append(*h, x.(Request)) }
func (h *reqHeap) Pop() any {
	p := *h
	old := p[len(p)-1]
	*h = p[:len(p)-1]
	return old
}
