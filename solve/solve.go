// Package solve resolves one version per reachable package under all constraints.
package solve

import (
	"sort"
	"sync"

	"ontology/graph"
	"ontology/rng"
	"ontology/ver"
)

// Solver runs deterministic backtracking searches over a graph.
type Solver struct {
	g      *graph.Graph
	budget int // 0 means unlimited

	mu    sync.Mutex
	tried int // attempts made by the latest Solve call; never part of the API result
}

// New creates a solver for g.
func New(g *graph.Graph) *Solver { return &Solver{g: g} }

// WithBudget returns a solver copy limited to at most n attempted package@version
// combinations; n <= 0 means unlimited.
func (s *Solver) WithBudget(n int) *Solver {
	return &Solver{g: s.g, budget: n}
}

// Tried reports the number of package@version combinations last Solve tried.
func (s *Solver) Tried() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tried
}

// Root is one root requirement: package Pkg must satisfy Spec.
type Root struct {
	Pkg  string
	Spec string
}

type entry struct {
	r       rng.Range
	lowProv []Edge
	hiProv  []Edge
}

func copyChain(prefix []Edge, tail ...Edge) []Edge {
	out := make([]Edge, 0, len(prefix)+len(tail))
	out = append(out, prefix...)
	out = append(out, tail...)
	return out
}

type searcher struct {
	g       *graph.Graph
	reach   map[string]bool
	domains map[string]*entry
	chosen  map[string]string
	tried   int
	budget  int
}

// Solve searches for a consistent selection of the reachable packages.
func (s *Solver) Solve(roots []Root) (map[string]string, error) {
	rr := sortedRoots(roots)
	st := &searcher{g: s.g, budget: s.budget,
		reach: map[string]bool{}, domains: map[string]*entry{},
		chosen: map[string]string{}}

	if err := st.buildReach(rr); err != nil {
		return nil, err
	}
	for pkg := range st.reach {
		st.domains[pkg] = &entry{r: rng.All()}
	}
	for _, root := range rr {
		rg, err := rng.Parse(root.Spec)
		if err != nil {
			return nil, err
		}
		e := rootEdge(root)
		if _, err := st.narrow(root.Pkg, rg, chain(e), chain(e)); err != nil {
			return nil, err
		}
	}

	sol, err := st.backtrack()
	s.mu.Lock()
	s.tried = st.tried
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return sol, nil
}

func sortedRoots(roots []Root) []Root {
	rr := append([]Root(nil), roots...)
	sort.Slice(rr, func(i, j int) bool { return rr[i].Pkg < rr[j].Pkg })
	return rr
}

func rootEdge(r Root) Edge { return Edge{Pkg: r.Pkg, Spec: r.Spec} }

func chain(e Edge) []Edge { return []Edge{e} }

func (st *searcher) buildReach(roots []Root) error {
	var queue []string
	for _, r := range roots {
		if !st.g.Has(r.Pkg) {
			return &UnknownPackageError{Pkg: r.Pkg, DeclBy: rootEdge(r)}
		}
		if !st.reach[r.Pkg] {
			st.reach[r.Pkg] = true
			queue = append(queue, r.Pkg)
		}
	}
	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]
		for _, v := range st.g.Versions(pkg) {
			for _, d := range st.g.Deps(pkg, v) {
				if !st.g.Has(d.Pkg) {
					e := Edge{From: pkg, FromVer: v, Pkg: d.Pkg, Spec: d.Spec}
					return &UnknownPackageError{Pkg: d.Pkg, DeclBy: e}
				}
				if !st.reach[d.Pkg] {
					st.reach[d.Pkg] = true
					queue = append(queue, d.Pkg)
				}
			}
		}
	}
	return nil
}
