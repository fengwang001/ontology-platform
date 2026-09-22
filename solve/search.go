package solve

import (
	"sort"

	"ontology/rng"
	"ontology/ver"
)

type snapshot struct {
	domains map[string]*entry
	chosen  map[string]string
}

func (st *searcher) snapshot() snapshot {
	d := make(map[string]*entry, len(st.domains))
	for k, v := range st.domains {
		cp := &entry{r: v.r, lowProv: dup(v.lowProv), hiProv: dup(v.hiProv)}
		d[k] = cp
	}
	c := make(map[string]string, len(st.chosen))
	for k, v := range st.chosen {
		c[k] = v
	}
	return snapshot{domains: d, chosen: c}
}

func (st *searcher) apply(s snapshot) {
	d := make(map[string]*entry, len(s.domains))
	for k, v := range s.domains {
		d[k] = &entry{r: v.r, lowProv: dup(v.lowProv), hiProv: dup(v.hiProv)}
	}
	c := make(map[string]string, len(s.chosen))
	for k, v := range s.chosen {
		c[k] = v
	}
	st.domains = d
	st.chosen = c
}

func (st *searcher) backtrack() (map[string]string, error) {
	pkg := st.pickPkg()
	if pkg == "" {
		return map[string]string{}, nil
	}
	base := st.snapshot()
	versions := st.g.Versions(pkg)
	var conflict *ConflictError
	if c := st.checkCandidates(); c != nil {
		return nil, c
	}
	for i := len(versions) - 1; i >= 0; i-- {
		v := versions[i]
		pv, _ := ver.Parse(v)
		if !base.domains[pkg].r.Contains(pv) {
			continue
		}
		st.apply(base)
		st.tried++
		if st.budget > 0 && st.tried > st.budget {
			return nil, ErrSearchBudget
		}
		st.chosen[pkg] = v
		if err := st.propagate(pkg, v); err != nil {
			conflict = asConflict(err)
			continue
		}
		if err := st.checkChosen(); err != nil {
			conflict = err
			continue
		}
		if err := st.checkCandidates(); err != nil {
			conflict = err
			continue
		}
		rest, err := st.backtrack()
		if err == ErrSearchBudget {
			return nil, err
		}
		if err != nil {
			conflict = asConflict(err)
			continue
		}
		rest[pkg] = v
		return rest, nil
	}
	st.apply(base)
	if conflict != nil {
		return nil, conflict
	}
	// No version of pkg itself fit the domain: report it with the domain
	// constraints from the last attempted state by re-deriving from base's
	// narrowed entry.
	if !anyFits(pkg, st.domains[pkg].r, st.g) {
		e := st.domains[pkg]
		return nil, &ConflictError{Pkg: pkg, Low: dup(e.lowProv), High: dup(e.hiProv),
			NoCandidate: !e.r.IsEmpty(), Nearest: nearest(pkg, e.r, st.g)}
	}
	e := st.domains[pkg]
	return nil, &ConflictError{Pkg: pkg, Low: dup(e.lowProv), High: dup(e.hiProv)}
}

func anyFits(pkg string, r rng.Range, g interface{ Versions(string) []string }) bool {
	for _, vs := range g.Versions(pkg) {
		pv, _ := ver.Parse(vs)
		if r.Contains(pv) {
			return true
		}
	}
	return false
}

func asConflict(err error) *ConflictError {
	if ce, ok := err.(*ConflictError); ok {
		return ce
	}
	return &ConflictError{}
}

// propagate applies every constraint declared by pkg@version.
func (st *searcher) propagate(pkg, version string) error {
	for _, d := range st.g.Deps(pkg, version) {
		if !st.reach[d.Pkg] {
			continue
		}
		r, ok := st.g.Constraint(pkg, version, d.Pkg)
		if !ok {
			continue
		}
		e := Edge{From: pkg, FromVer: version, Pkg: d.Pkg, Spec: d.Spec}
		var low, high []Edge
		if _, ok, _ := r.Low(); ok {
			low = []Edge{e}
		}
		if _, ok, _ := r.High(); ok {
			high = []Edge{e}
		}
		if _, err := st.narrow(d.Pkg, r, low, high); err != nil {
			return err
		}
	}
	return nil
}

// checkChosen verifies every selection still fits its (possibly narrowed)
// domain; a new constraint can invalidate an earlier choice in a dependency
// cycle, and the stale choice must be rolled back.
func (st *searcher) checkChosen() *ConflictError {
	names := make([]string, 0, len(st.chosen))
	for pkg := range st.chosen {
		names = append(names, pkg)
	}
	sort.Strings(names)
	for _, pkg := range names {
		version := st.chosen[pkg]
		pv, _ := ver.Parse(version)
		if st.domains[pkg].r.Contains(pv) {
			continue
		}
		e := st.domains[pkg]
		return &ConflictError{Pkg: pkg, Low: e.lowProv, High: e.hiProv}
	}
	return nil
}

// checkCandidates detects non-empty intervals containing no registered version;
// running it after every propagation is the main conflict-driven prune.
func (st *searcher) checkCandidates() *ConflictError {
	names := make([]string, 0, len(st.reach))
	for pkg := range st.reach {
		if _, done := st.chosen[pkg]; !done {
			names = append(names, pkg)
		}
	}
	sort.Strings(names)
	for _, pkg := range names {
		var fit bool
		for _, vs := range st.g.Versions(pkg) {
			pv, _ := ver.Parse(vs)
			if st.domains[pkg].r.Contains(pv) {
				fit = true
				break
			}
		}
		if fit {
			continue
		}
		e := st.domains[pkg]
		c := &ConflictError{Pkg: pkg, Low: dup(e.lowProv), High: dup(e.hiProv),
			NoCandidate: !e.r.IsEmpty(), Nearest: nearest(pkg, e.r, st.g)}
		return c
	}
	return nil
}

// nearest returns the greatest registered version not above the lower bound
// (or the lowest registered version), purely for human-readable output.
func nearest(pkg string, r rng.Range, g interface{ Versions(string) []string }) string {
	versions := g.Versions(pkg)
	if lv, ok, _ := r.Low(); ok {
		var best string
		var bestV ver.Version
		for _, vs := range versions {
			pv, _ := ver.Parse(vs)
			if ver.Compare(pv, lv) <= 0 &&
				(best == "" || ver.Compare(pv, bestV) > 0) {
				best, bestV = vs, pv
			}
		}
		if best != "" {
			return best
		}
	}
	if len(versions) > 0 {
		return versions[0]
	}
	return ""
}

// pickPkg chooses an unresolved reachable package deterministically: packages
// whose domain has already been narrowed (constrained MRV) come first, then
// lexicographic order. Constrained-first makes conflicts surface on the
// package a chain is actually about, and prunes earlier.
func (st *searcher) pickPkg() string {
	var constrained, free []string
	for pkg := range st.reach {
		if _, done := st.chosen[pkg]; !done {
			if st.isConstrained(pkg) {
				constrained = append(constrained, pkg)
			} else {
				free = append(free, pkg)
			}
		}
	}
	sort.Strings(constrained)
	sort.Strings(free)
	if len(constrained) > 0 {
		return constrained[0]
	}
	if len(free) == 0 {
		return ""
	}
	return free[0]
}

func (st *searcher) isConstrained(pkg string) bool {
	_, lo, _ := st.domains[pkg].r.Low()
	_, hi, _ := st.domains[pkg].r.High()
	return lo || hi
}
