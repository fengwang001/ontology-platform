package epaxos

import "sort"

// executeLocked executes every executable committed instance and returns the
// newly executed instances in deterministic order.
//
// An instance is executable iff every instance reachable from it along
// dependency edges (over committed-but-unexecuted instances) is committed;
// deps on already-executed instances are satisfied and form no edges, while
// deps on uncommitted instances block the instance and, transitively, its
// dependents.
//
// Executable instances are partitioned into SCCs of the dependency graph.
// Components are emitted dependencies-first: among the ready components
// (all components they depend on already emitted), the one whose sorted
// member list has the smallest first member (by seq, R, I) goes first.
// Members inside a component are emitted in (seq, R, I) ascending order.
func (s *Scheduler) executeLocked() []Instance {
	if s.pending == 0 {
		return nil
	}

	// Collect committed-but-unexecuted nodes and intra-graph edges u -> d.
	nodes := make([]Instance, 0, s.pending)
	edges := make(map[Instance][]Instance, s.pending)
	blocked := make(map[Instance]bool, s.pending)
	for inst, rec := range s.records {
		if rec.executed {
			continue
		}
		nodes = append(nodes, inst)
		for _, d := range rec.deps {
			drec, committed := s.records[d]
			switch {
			case !committed:
				blocked[inst] = true // dep not committed yet
			case !drec.executed:
				edges[inst] = append(edges[inst], d)
			}
		}
	}

	// Propagate blockage backwards: anything that can reach a blocked node
	// is blocked too.
	rev := make(map[Instance][]Instance, len(nodes))
	for u, ds := range edges {
		for _, d := range ds {
			rev[d] = append(rev[d], u)
		}
	}
	stack := make([]Instance, 0, len(blocked))
	for inst := range blocked {
		stack = append(stack, inst)
	}
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, w := range rev[u] {
			if !blocked[w] {
				blocked[w] = true
				stack = append(stack, w)
			}
		}
	}

	// Executable subgraph: nodes not blocked. Their edges can only lead to
	// other executable nodes.
	exec := make([]Instance, 0, len(nodes)-len(blocked))
	for _, inst := range nodes {
		if !blocked[inst] {
			exec = append(exec, inst)
		}
	}
	if len(exec) == 0 {
		return nil
	}

	comp := tarjan(exec, edges)

	// Group members per component and sort each by (seq, R, I).
	members := make(map[int][]Instance)
	for _, inst := range exec {
		c := comp[inst]
		members[c] = append(members[c], inst)
	}
	for c := range members {
		s.sortBySeqInst(members[c])
	}

	// Component dependency edges: c -> c' means c depends on c'.
	compDeps := make(map[int]map[int]struct{})
	for _, inst := range exec {
		c := comp[inst]
		for _, d := range edges[inst] {
			cd := comp[d]
			if cd == c {
				continue
			}
			if compDeps[c] == nil {
				compDeps[c] = make(map[int]struct{})
			}
			compDeps[c][cd] = struct{}{}
		}
	}

	// Emit ready components, smallest first member first.
	remaining := make(map[int]bool, len(members))
	for c := range members {
		remaining[c] = true
	}
	emitted := make(map[int]bool, len(members))
	out := make([]Instance, 0, len(exec))
	for len(remaining) > 0 {
		best := -1
		for c := range remaining {
			ready := true
			for dep := range compDeps[c] {
				if !emitted[dep] {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			if best == -1 || s.lessInst(members[c][0], members[best][0]) {
				best = c
			}
		}
		for _, inst := range members[best] {
			out = append(out, inst)
			s.records[inst].executed = true
		}
		s.pending -= len(members[best])
		delete(remaining, best)
		emitted[best] = true
	}
	return out
}

func (s *Scheduler) lessInst(a, b Instance) bool {
	ra, rb := s.records[a], s.records[b]
	if ra.seq != rb.seq {
		return ra.seq < rb.seq
	}
	if a.R != b.R {
		return a.R < b.R
	}
	return a.I < b.I
}

func (s *Scheduler) sortBySeqInst(insts []Instance) {
	sort.Slice(insts, func(a, b int) bool { return s.lessInst(insts[a], insts[b]) })
}

// tarjan assigns each node in nodes an SCC id using Tarjan's algorithm.
// Ids are assigned in reverse finish order; only equality of ids matters.
func tarjan(nodes []Instance, edges map[Instance][]Instance) map[Instance]int {
	index := make(map[Instance]int, len(nodes))
	low := make(map[Instance]int, len(nodes))
	onStack := make(map[Instance]bool, len(nodes))
	comp := make(map[Instance]int, len(nodes))
	var stk []Instance
	counter := 0
	ncomp := 0

	var strongconnect func(v Instance)
	strongconnect = func(v Instance) {
		index[v] = counter
		low[v] = counter
		counter++
		stk = append(stk, v)
		onStack[v] = true
		for _, w := range edges[v] {
			if _, ok := index[w]; !ok {
				strongconnect(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] {
				if index[w] < low[v] {
					low[v] = index[w]
				}
			}
		}
		if low[v] == index[v] {
			for {
				w := stk[len(stk)-1]
				stk = stk[:len(stk)-1]
				onStack[w] = false
				comp[w] = ncomp
				if w == v {
					break
				}
			}
			ncomp++
		}
	}
	for _, v := range nodes {
		if _, ok := index[v]; !ok {
			strongconnect(v)
		}
	}
	return comp
}
