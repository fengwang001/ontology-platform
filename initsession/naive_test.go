package initsession

import (
	"fmt"
	"sort"
)

// naiveModel is an intentionally simple, independently written
// reference implementation of the specification. It mirrors the
// literal rule ("each round rescan all remaining units in source
// order") without any memoization or priority queue, and is used only
// by the differential test.

type naiveDecl struct {
	isFunc bool
	name   string   // function name, or "" for units
	vars   []string // unit lhs
	refs   []string
	order  int // global registration order
}

type naiveModel struct {
	pre   map[string]bool
	decls []*naiveDecl
	units []*naiveDecl
	funcs map[string]*naiveDecl
}

func newNaive(pre []string) *naiveModel {
	m := &naiveModel{pre: map[string]bool{}, funcs: map[string]*naiveDecl{}}
	for _, p := range pre {
		m.pre[p] = true
	}
	return m
}

// apply mirrors one accepted Session operation.
func (m *naiveModel) apply(isFunc bool, name string, vars, refs []string) {
	d := &naiveDecl{
		isFunc: isFunc,
		name:   name,
		vars:   append([]string(nil), vars...),
		refs:   append([]string(nil), refs...),
		order:  len(m.decls),
	}
	m.decls = append(m.decls, d)
	if isFunc {
		m.funcs[name] = d
	} else {
		m.units = append(m.units, d)
	}
}

type naiveResult struct {
	order      []int
	deps       map[int][]string
	undecl     *naiveDecl
	undeclName string
	cycleVars  []string
}

// reachableVars expands references to the set of variable names
// reached, with an explicit DFS over functions. includeOwn controls
// whether the consuming unit's own variables are kept: scheduling must
// keep a self edge (a self reference is a cycle), the reported dep set
// must exclude them.
func (m *naiveModel) reachableVars(refs []string, skipUnitVars map[string]bool, includeOwn bool) map[string]bool {
	out := map[string]bool{}
	visiting := map[string]bool{}
	var visitRef func(r string)
	var visitFn func(f *naiveDecl)
	visitRef = func(r string) {
		if m.pre[r] {
			return
		}
		if f, ok := m.funcs[r]; ok {
			visitFn(f)
			return
		}
		// Otherwise treat as a declared variable; declaration existence
		// is checked elsewhere.
		if includeOwn || !skipUnitVars[r] {
			out[r] = true
		}
	}
	visitFn = func(f *naiveDecl) {
		if visiting[f.name] {
			return
		}
		visiting[f.name] = true
		for _, r := range f.refs {
			visitRef(r)
		}
		delete(visiting, f.name)
	}
	for _, r := range refs {
		visitRef(r)
	}
	return out
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (m *naiveModel) solve() *naiveResult {
	res := &naiveResult{deps: map[int][]string{}}

	// Undeclared: earliest declaration (global order) with any unresolved
	// reference; report lexicographically smallest such reference.
	var worst *naiveDecl
	var worstName string
	for _, d := range m.decls {
		var bad []string
		for _, r := range d.refs {
			if m.pre[r] {
				continue
			}
			if _, ok := m.funcs[r]; ok {
				continue
			}
			if m.isVar(r) {
				continue
			}
			bad = append(bad, r)
		}
		if len(bad) > 0 {
			sort.Strings(bad)
			worst = d
			worstName = bad[0]
			break
		}
	}
	if worst != nil {
		res.undecl = worst
		res.undeclName = worstName
		return res
	}

	// Dep sets per unit.
	depUnits := make([]map[int]bool, len(m.units))
	for ui, u := range m.units {
		own := map[string]bool{}
		for _, v := range u.vars {
			own[v] = true
		}
		allVars := m.reachableVars(u.refs, own, true)
		reported := m.reachableVars(u.refs, own, false)
		res.deps[ui] = sortedKeys(reported)
		set := map[int]bool{}
		for v := range allVars {
			set[m.varOwner(v)] = true
		}
		depUnits[ui] = set
	}

	// Literal round rule: rescan all remaining units each time, take the
	// first (source order) whose unit-level deps are all initialized.
	done := make([]bool, len(m.units))
	doneCount := 0
	for {
		picked := -1
		for ui := range m.units {
			if done[ui] {
				continue
			}
			ready := true
			for d := range depUnits[ui] {
				if !done[d] {
					ready = false
					break
				}
			}
			if ready {
				picked = ui
				break
			}
		}
		if picked == -1 {
			break
		}
		done[picked] = true
		res.order = append(res.order, picked)
		doneCount++
	}

	if doneCount != len(m.units) {
		for ui, u := range m.units {
			if done[ui] {
				continue
			}
			for _, v := range u.vars {
				if v != "_" {
					res.cycleVars = append(res.cycleVars, v)
				}
			}
		}
	}
	return res
}

func (m *naiveModel) isVar(name string) bool {
	for _, u := range m.units {
		for _, v := range u.vars {
			if v == name {
				return true
			}
		}
	}
	return false
}

func (m *naiveModel) varOwner(name string) int {
	for ui, u := range m.units {
		for _, v := range u.vars {
			if v == name {
				return ui
			}
		}
	}
	return -1
}

func (r *naiveResult) label() string {
	switch {
	case r.undecl != nil:
		return fmt.Sprintf("undeclared in %v: %s", r.undecl.name, r.undeclName)
	case r.cycleVars != nil:
		return fmt.Sprintf("cycle %v", r.cycleVars)
	default:
		return fmt.Sprintf("ok order=%v", r.order)
	}
}
