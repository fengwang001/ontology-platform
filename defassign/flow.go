package defassign

// slice is one path-slice of the program state: the decision tokens taken
// to arrive here and the variables definitely assigned along this
// particular path. A program point's state is a list of slices, one per
// distinct arriving path; a variable is definitely assigned at a point iff
// it is assigned in every slice.
type slice struct {
	path []string
	asg  map[string]struct{}
}

// Stats instruments the analysis so the complexity guarantees are
// verifiable in tests.
type Stats struct {
	// ReadChecks counts slice inspections performed at read points. It is
	// bounded by the number of paths merged at each read and never grows
	// with the total program length.
	ReadChecks int
	// MergeOps counts slice deposits into merge points (nodes with two or
	// more non-back predecessors). Merges never iterate variables, so this
	// never grows with the number of variables uninvolved in the merge.
	MergeOps int
}

// flowResult holds the outcome of both dataflow passes.
type flowResult struct {
	inState [][]slice // per-node incoming path-slices (empty = unreachable)
	liveOut []map[string]bool
	stats   Stats
}

// analyze runs the forward definite-assignment pass and the backward
// liveness pass over the CFG.
func analyze(reg *Registry, g *cfg, entries []openEdge) flowResult {
	n := reg.NumNodes()
	inState := make([][]slice, n)
	var stats Stats

	// Seed the root entries: one empty path per entry point.
	for _, e := range entries {
		inState[e.from] = append(inState[e.from], slice{
			path: e.token,
			asg:  make(map[string]struct{}),
		})
	}

	// Merge points: nodes with at least two non-back predecessors.
	predCount := make([]int, n)
	for u := 0; u < n; u++ {
		for _, e := range g.succ[u] {
			if !e.back {
				predCount[e.to]++
			}
		}
	}

	// The graph without back edges is a DAG (structured control flow), so a
	// topological order exists; process nodes in that order.
	order := topoOrder(g, n)
	for _, u := range order {
		st := inState[u]
		if len(st) == 0 {
			continue // unreachable: no diagnostics, no propagation
		}
		node := reg.Node(u)
		out := st
		switch node.Kind {
		case KindRead:
			stats.ReadChecks += len(st)
		case KindAssign:
			out = assignSlices(st, node.Var)
		}
		for _, e := range g.succ[u] {
			if e.back {
				continue
			}
			if predCount[e.to] >= 2 {
				stats.MergeOps += len(out)
			}
			for _, s := range out {
				inState[e.to] = append(inState[e.to], slice{
					path: concatToken(s.path, e.token),
					asg:  s.asg,
				})
			}
		}
	}

	liveOut := liveness(reg, g)
	return flowResult{inState: inState, liveOut: liveOut, stats: stats}
}

// assignSlices returns st with v added to every slice's assigned set. Maps
// are shared immutably and copied on write.
func assignSlices(st []slice, v string) []slice {
	out := make([]slice, len(st))
	for i, s := range st {
		if _, ok := s.asg[v]; ok {
			out[i] = s
			continue
		}
		asg := make(map[string]struct{}, len(s.asg)+1)
		for k := range s.asg {
			asg[k] = struct{}{}
		}
		asg[v] = struct{}{}
		out[i] = slice{path: s.path, asg: asg}
	}
	return out
}

// topoOrder returns a deterministic topological order of the CFG with back
// edges removed.
func topoOrder(g *cfg, n int) []int {
	indeg := make([]int, n)
	for u := 0; u < n; u++ {
		for _, e := range g.succ[u] {
			if !e.back {
				indeg[e.to]++
			}
		}
	}
	var ready []int
	for u := 0; u < n; u++ {
		if indeg[u] == 0 {
			ready = append(ready, u)
		}
	}
	order := make([]int, 0, n)
	for len(ready) > 0 {
		u := ready[0]
		ready = ready[1:]
		order = append(order, u)
		for _, e := range g.succ[u] {
			if e.back {
				continue
			}
			indeg[e.to]--
			if indeg[e.to] == 0 {
				ready = append(ready, e.to)
			}
		}
	}
	return order
}

// liveness computes, for every node, the set of variables read on some
// continuation before being reassigned. Back edges are followed, so an
// assignment inside a loop is used if a later iteration may read it.
func liveness(reg *Registry, g *cfg) []map[string]bool {
	n := reg.NumNodes()
	liveIn := make([]map[string]bool, n)
	liveOut := make([]map[string]bool, n)
	for i := range liveIn {
		liveIn[i] = make(map[string]bool)
		liveOut[i] = make(map[string]bool)
	}
	for changed := true; changed; {
		changed = false
		for u := n - 1; u >= 0; u-- {
			out := make(map[string]bool)
			for _, e := range g.succ[u] {
				for v := range liveIn[e.to] {
					out[v] = true
				}
			}
			in := make(map[string]bool, len(out)+1)
			node := reg.Node(u)
			kills := node.Kind == KindAssign
			for v := range out {
				if kills && node.Var == v {
					continue
				}
				in[v] = true
			}
			if node.Kind == KindRead {
				in[node.Var] = true
			}
			if !equalSet(in, liveIn[u]) || !equalSet(out, liveOut[u]) {
				liveIn[u] = in
				liveOut[u] = out
				changed = true
			}
		}
	}
	return liveOut
}

func equalSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
