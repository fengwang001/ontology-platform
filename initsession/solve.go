package initsession

import (
	"container/heap"
	"fmt"
	"sort"
)

// snapshot is an immutable point-in-time view of accepted declarations.
// It is built under RLock and analyzed without holding any lock, so
// concurrent registrations never disturb an in-flight solve.
//
// Maps are shallow-copied; the backing unit/fn objects are never
// mutated after publication (registration appends new objects, it never
// edits accepted ones), so sharing them is safe.
type snapshot struct {
	predeclared map[string]struct{}
	names       map[string]nameInfo
	units       []*unit
	functions   []*fn
	totalRefs   int
	decls       []decl
}

func (s *Session) snapshotLocked() *snapshot {
	names := make(map[string]nameInfo, len(s.names))
	for n, info := range s.names {
		names[n] = info
	}
	pre := make(map[string]struct{}, len(s.predeclared))
	for n := range s.predeclared {
		pre[n] = struct{}{}
	}
	units := make([]*unit, len(s.units))
	copy(units, s.units)
	fns := make([]*fn, len(s.functions))
	copy(fns, s.functions)
	decls := make([]decl, len(s.decls))
	copy(decls, s.decls)
	return &snapshot{
		predeclared: pre,
		names:       names,
		units:       units,
		functions:   fns,
		totalRefs:   s.totalRefs,
		decls:       decls,
	}
}

// firstUndeclared scans every accepted declaration in registration
// order and returns the earliest declaration that references an
// identifier that is neither a declared variable/function nor
// predeclared, together with the lexicographically smallest offending
// reference of that declaration. Returns ok=false when every reference
// resolves.
func (snap *snapshot) firstUndeclared() (unitIdx int, fnName string, name string, ok bool) {
	for _, d := range snap.decls {
		var refs []string
		switch d.kind {
		case declUnit:
			refs = snap.units[d.index].refs
		case declFunction:
			refs = snap.functions[d.index].refs
		}
		if n, bad := snap.undeclaredIn(refs); bad {
			if d.kind == declUnit {
				return d.index, "", n, true
			}
			return -1, snap.functions[d.index].name, n, true
		}
	}
	return 0, "", "", false
}

func (snap *snapshot) undeclaredIn(refs []string) (string, bool) {
	var first string
	found := false
	for _, ref := range refs {
		if _, ok := snap.names[ref]; ok {
			continue
		}
		if _, ok := snap.predeclared[ref]; ok {
			continue
		}
		if !found || ref < first {
			first = ref
			found = true
		}
	}
	return first, found
}

// solve runs the full analysis on an immutable snapshot.
func (snap *snapshot) solve() (*Result, Stats, error) {
	st := &Stats{Units: len(snap.units), Functions: len(snap.functions), Refs: snap.totalRefs}

	// Error priority: any undeclared reference (across all declarations,
	// including unused functions) beats any initialization cycle.
	if idx, fnName, name, bad := snap.firstUndeclared(); bad {
		return nil, *st, &Error{
			Kind:      KindUndeclared,
			UnitIndex: idx,
			Function:  fnName,
			Name:      name,
			Message:   fmt.Sprintf("undeclared identifier %q", name),
		}
	}

	cs := &closureState{snap: snap, st: st}
	cs.build()
	return snap.schedule(cs, st)
}

// schedule runs the deterministic selection rule.
//
// "Repeatedly pick, among units not yet initialized, the source-earliest
// unit whose depended-on variables are all initialized."
//
// The rule is a Kahn process over unit-level dependency edges with a
// source-order priority queue for tie breaking. When a unit completes,
// it notifies exactly the units that depend on it (reverse edges); a
// notified unit is re-checked once. There is no rescanning of all
// remaining units per round: initial readiness costs one check per
// unit, and afterwards each reverse edge yields at most one check.
func (snap *snapshot) schedule(cs *closureState, st *Stats) (*Result, Stats, error) {
	n := len(snap.units)

	// depNames[u] is the reported dependency set (names ascending, own
	// variables and blanks excluded). remaining[u] counts distinct
	// uncompleted units that provide variables u depends on.
	depNames := make([][]string, n)
	remaining := make([]int, n)
	// dependents[j] are units that depend on unit j (reverse edges).
	dependents := make([][]int, n)

	for _, u := range snap.units {
		depIDs, depsOn := cs.unitClosure(u)
		names := make([]string, 0, len(depIDs))
		for _, id := range depIDs {
			names = append(names, cs.varName[id])
		}
		sort.Strings(names)
		depNames[u.index] = names
		remaining[u.index] = len(depsOn)
		for j := range depsOn {
			dependents[j] = append(dependents[j], u.index)
		}
	}

	ready := &unitHeap{}
	heap.Init(ready)
	for i := 0; i < n; i++ {
		st.ReadyChecks++
		if remaining[i] == 0 {
			heap.Push(ready, i)
		}
	}

	completed := make([]bool, n)
	order := make([]int, 0, n)
	for ready.Len() > 0 {
		u := heap.Pop(ready).(int)
		completed[u] = true
		order = append(order, u)
		for _, w := range dependents[u] {
			if !completed[w] {
				st.ReverseNotifications++
				st.ReadyChecks++
				remaining[w]--
				if remaining[w] == 0 {
					heap.Push(ready, w)
				}
			}
		}
	}

	res := &Result{
		Order:        order,
		Dependencies: make([]UnitDeps, n),
	}
	for _, u := range snap.units {
		res.Dependencies[u.index] = UnitDeps{
			UnitIndex: u.index,
			Variables: append([]string(nil), u.vars...),
			Deps:      depNames[u.index],
		}
	}

	if len(order) != n {
		// Initialization cycle: every non-completed unit is part of the
		// remaining dependency closure (a unit with no path into a cycle
		// would itself have completed). Report all of its non-blank
		// variables in source order: first by unit registration order,
		// then by left-hand-side position.
		var names []string
		for _, u := range snap.units {
			if completed[u.index] {
				continue
			}
			for _, v := range u.vars {
				if !IsBlank(v) {
					names = append(names, v)
				}
			}
		}
		return res, *st, &Error{
			Kind:      KindInitCycle,
			UnitIndex: -1,
			Names:     names,
			Message:   fmt.Sprintf("initialization cycle involves %d variable(s)", len(names)),
		}
	}

	return res, *st, nil
}
