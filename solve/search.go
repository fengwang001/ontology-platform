package solve

import (
	"errors"
	"fmt"
	"sort"

	"ontology/graph"
	"ontology/ver"
)

// search holds the per-Solve-call mutable state. Nothing here is shared
// between concurrent Solve calls.
type search struct {
	g      *graph.Graph
	max    int64
	tried  int64
	assign map[string]ver.Version
	imp    map[string][]imposed
	best   string
}

func (sch *search) run() (Solution, error) {
	pkg, ok := sch.next()
	if !ok {
		sol := make(Solution, len(sch.assign))
		for k, v := range sch.assign {
			sol[k] = v
		}
		return sol, nil
	}
	cands := sch.feasible(pkg)
	if len(cands) == 0 {
		sch.record(pkg)
		return nil, errConflict
	}
	for _, v := range cands {
		sch.tried++
		if sch.tried > sch.max {
			return nil, ErrBudgetExceeded
		}
		undo, err := sch.tryAssign(pkg, v)
		if errors.Is(err, errConflict) {
			continue
		}
		if err != nil {
			return nil, err
		}
		sol, err := sch.run()
		if err == nil || errors.Is(err, ErrBudgetExceeded) || errors.Is(err, ErrUnknownPackage) {
			return sol, err
		}
		undo()
	}
	return nil, errConflict
}

// next picks the unassigned, constrained package with the fewest feasible
// versions (ties broken by name), keeping the search deterministic.
func (sch *search) next() (string, bool) {
	pkgs := make([]string, 0, len(sch.imp))
	for p := range sch.imp {
		if _, done := sch.assign[p]; !done && len(sch.imp[p]) > 0 {
			pkgs = append(pkgs, p)
		}
	}
	sort.Strings(pkgs)
	best, bestN := "", -1
	for _, p := range pkgs {
		n := len(sch.feasible(p))
		if bestN < 0 || n < bestN {
			best, bestN = p, n
		}
	}
	return best, bestN >= 0
}

// feasible returns the registered versions of pkg satisfying every
// imposed constraint, in descending order.
func (sch *search) feasible(pkg string) []ver.Version {
	var out []ver.Version
	for _, v := range sch.g.Versions(pkg) {
		ok := true
		for _, im := range sch.imp[pkg] {
			if !im.con.Contains(v) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, v)
		}
	}
	return out
}

// tryAssign tentatively selects pkg@v and propagates its constraints.
// On conflict it records an explanation, undoes itself and returns
// errConflict; the returned func undoes a successful assignment.
func (sch *search) tryAssign(pkg string, v ver.Version) (func(), error) {
	sch.assign[pkg] = v
	var touched []string
	undo := func() {
		delete(sch.assign, pkg)
		for _, t := range touched {
			sch.imp[t] = sch.imp[t][:len(sch.imp[t])-1]
			if len(sch.imp[t]) == 0 {
				delete(sch.imp, t)
			}
		}
	}
	for _, d := range sch.g.Dependencies(pkg, v.String()) {
		if !sch.g.Has(d.Target) {
			undo()
			return nil, fmt.Errorf("%w: %q (required by %s@%s)", ErrUnknownPackage, d.Target, pkg, v)
		}
		sch.imp[d.Target] = append(sch.imp[d.Target],
			imposed{pkg: pkg, ver: v.String(), raw: d.Con.String(), con: d.Con})
		touched = append(touched, d.Target)
		if av, ok := sch.assign[d.Target]; ok {
			if !d.Con.Contains(av) {
				sch.record(d.Target)
				undo()
				return nil, errConflict
			}
		} else if len(sch.feasible(d.Target)) == 0 {
			sch.record(d.Target)
			undo()
			return nil, errConflict
		}
	}
	return undo, nil
}
