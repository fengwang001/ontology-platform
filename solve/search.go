package solve

import (
	"sort"

	"ontology/graph"
	"ontology/rng"
	"ontology/ver"
)

type search struct {
	g          *graph.Graph
	maxAttempt int
	attempts   int

	// active 仅含从根可达、且已被某条约束引入的包；域 = entries 过滤 candidates。
	pkgs     map[string]bool
	entries  map[string][]rng.Entry
	chosen   map[string]ver.Version
	rootRefs []rng.Entry
}

// rootEntry 用来源 Pkg=="" 标记根约束。
func rootEntry(r Root) rng.Entry {
	rr, _ := rng.Parse(r.Constraint)
	return rng.Entry{Range: rr, Origin: rng.Origin{Target: r.Package, Raw: r.Constraint}}
}

func (s *Solver) solve(roots []Root, maxAttempt int) (*Solution, *search, error) {
	st := &search{
		g: s.g, maxAttempt: maxAttempt,
		pkgs: map[string]bool{}, entries: map[string][]rng.Entry{},
		chosen: map[string]ver.Version{},
	}
	sortedRoots := make([]Root, len(roots))
	copy(sortedRoots, roots)
	sort.SliceStable(sortedRoots, func(i, j int) bool { return sortedRoots[i].Package < sortedRoots[j].Package })
	for _, r := range sortedRoots {
		if _, err := rng.Parse(r.Constraint); err != nil {
			return nil, nil, err
		}
		if !s.g.HasPackage(r.Package) {
			return nil, nil, &UnknownPackageError{Package: r.Package}
		}
		e := rootEntry(r)
		st.rootRefs = append(st.rootRefs, e)
		st.addEntry(e)
	}
	if err := st.run(); err != nil {
		return nil, st, err
	}
	return &Solution{chosen: st.chosen, attempts: st.attempts}, st, nil
}

func (st *search) addEntry(e rng.Entry) {
	st.pkgs[e.Origin.Target] = true
	st.entries[e.Origin.Target] = append(st.entries[e.Origin.Target], e)
}

func (st *search) tick() error {
	st.attempts++
	if st.maxAttempt > 0 && st.attempts > st.maxAttempt {
		return ErrSearchBudget
	}
	return nil
}

func (st *search) run() error {
	// 1) 结构空区间：域必然为空，立即判定。
	for _, pkg := range st.sortedPkgs() {
		for _, pair := range incompatiblePairs(st.entries[pkg]) {
			return st.conflict(pkg, pair)
		}
	}
	// 2) 选下一个未指派包：包名升序，候选最少优先。
	pkg := st.pickPkg()
	if pkg == "" {
		return nil
	}
	cands := st.candidates(pkg)
	if len(cands) == 0 {
		return st.conflict(pkg, st.firstRejection(pkg))
	}
	// 3) 高版本优先尝试。
	var leafErr error
	for i := len(cands) - 1; i >= 0; i-- {
		v := cands[i]
		if err := st.tick(); err != nil {
			return err
		}
		for _, d := range st.g.Deps(graph.Ref{Pkg: pkg, V: v}) {
			if !st.g.HasPackage(d.Origin.Target) {
				return &UnknownPackageError{Package: d.Origin.Target}
			}
		}
		st.guess(pkg, v)
		if err := st.run(); err == nil {
			return nil
		} else if err == ErrSearchBudget {
			return err
		} else {
			leafErr = err
		}
		st.undoGuess(pkg, v)
	}
	// 所有候选都失败：以最低候选被拒原因给出确定性冲突。
	if leafErr != nil {
		return leafErr
	}
	return st.conflict(pkg, st.firstRejection(pkg))
}

func (st *search) guess(pkg string, v ver.Version) {
	st.chosen[pkg] = v
	deps := st.g.Deps(graph.Ref{Pkg: pkg, V: v})
	for _, d := range deps {
		st.addEntry(d)
		if _, ok := st.chosen[d.Origin.Target]; !ok {
			st.pkgs[d.Origin.Target] = true
		}
	}
}

func (st *search) undoGuess(pkg string, v ver.Version) {
	delete(st.chosen, pkg)
	deps := st.g.Deps(graph.Ref{Pkg: pkg, V: v})
	for _, d := range deps {
		t := d.Origin.Target
		es := st.entries[t]
		for i := len(es) - 1; i >= 0; i-- {
			if es[i].Origin.Pkg == pkg && es[i].Origin.V.Compare(v) == 0 {
				st.entries[t] = append(es[:i], es[i+1:]...)
				break
			}
		}
	}
	// 重新计算活跃集：仅保留根可达的包。
	st.reachable()
}

func (st *search) reachable() {
	live := map[string]bool{}
	var walk func(string)
	walk = func(pkg string) {
		if live[pkg] {
			return
		}
		live[pkg] = true
		if v, ok := st.chosen[pkg]; ok {
			for _, d := range st.g.Deps(graph.Ref{Pkg: pkg, V: v}) {
				walk(d.Origin.Target)
			}
		}
	}
	for _, e := range st.rootRefs {
		walk(e.Origin.Target)
	}
	for p := range st.pkgs {
		if !live[p] {
			delete(st.pkgs, p)
			delete(st.entries, p)
			delete(st.chosen, p)
		}
	}
}

func (st *search) pickPkg() string {
	best := ""
	bestN := 0
	for _, pkg := range st.sortedPkgs() {
		if _, done := st.chosen[pkg]; done {
			continue
		}
		n := len(st.candidates(pkg))
		if best == "" || n < bestN {
			best, bestN = pkg, n
		}
	}
	return best
}

func (st *search) sortedPkgs() []string {
	names := make([]string, 0, len(st.pkgs))
	for p := range st.pkgs {
		names = append(names, p)
	}
	sort.Strings(names)
	return names
}
