package bce

import "sort"

// Loop is a strongly connected cycle of blocks with (after validation) a
// unique entry, the header.
type Loop struct {
	Header    string
	Blocks    map[string]bool
	Entries   []string // blocks in the loop with a predecessor outside it
	Latches   []string // blocks in the loop with an edge to the header
	Assigned  map[string]bool
	Arrays    map[string]bool
	Ind       []Induction
	Preheader string // unique outside predecessor of the header, if any
}

// Induction describes a loop variable incremented by a fixed constant
// exactly once per iteration.
type Induction struct {
	Var   string
	Delta int
}

// cfg holds adjacency for analysis.
type cfg struct {
	succ map[string][]string
	pred map[string][]string
}

func buildCFG(p *Program) cfg {
	c := cfg{succ: map[string][]string{}, pred: map[string][]string{}}
	for _, b := range p.Blocks {
		for _, s := range successors(b.Terms[0]) {
			c.succ[b.Name] = append(c.succ[b.Name], s)
			c.pred[s] = append(c.pred[s], b.Name)
		}
		if _, ok := c.succ[b.Name]; !ok {
			c.succ[b.Name] = nil
		}
		if _, ok := c.pred[b.Name]; !ok {
			c.pred[b.Name] = nil
		}
	}
	return c
}

// findLoops returns the loops of the program as maximal SCCs that contain
// a cycle, sorted for determinism.
func findLoops(p *Program) []*Loop {
	c := buildCFG(p)
	sccs := tarjan(p, c)
	var loops []*Loop
	for _, sc := range sccs {
		cyclic := len(sc) > 1
		if !cyclic && len(sc) == 1 {
			for _, s := range c.succ[sc[0]] {
				if s == sc[0] {
					cyclic = true
				}
			}
		}
		if !cyclic {
			continue
		}
		in := map[string]bool{}
		for _, n := range sc {
			in[n] = true
		}
		lp := &Loop{Blocks: in, Assigned: map[string]bool{}, Arrays: map[string]bool{}}
		for _, n := range sc {
			out := false
			for _, pr := range c.pred[n] {
				if !in[pr] {
					out = true
				}
			}
			if out || n == p.Entry {
				lp.Entries = append(lp.Entries, n)
			}
			for _, instr := range p.Block(n).Instrs {
				switch instr.Kind {
				case OpAssignConst, OpAssign, OpAssignAdd:
					lp.Assigned[instr.Dst] = true
				case OpNewArray, OpAssignArray:
					lp.Arrays[instr.Arr] = true
				}
			}
		}
		sort.Strings(lp.Entries)
		loops = append(loops, lp)
	}
	sort.Slice(loops, func(i, j int) bool {
		hi, hj := "", ""
		if len(loops[i].Entries) > 0 {
			hi = loops[i].Entries[0]
		}
		if len(loops[j].Entries) > 0 {
			hj = loops[j].Entries[0]
		}
		return hi < hj
	})
	return loops
}

// finishLoops enriches validated loops (unique entry) with latches,
// preheader and induction variables. dom is the dominator map.
func finishLoops(p *Program, loops []*Loop, dom map[string]map[string]bool) {
	c := buildCFG(p)
	for _, lp := range loops {
		lp.Header = lp.Entries[0]
		for n := range lp.Blocks {
			for _, s := range c.succ[n] {
				if s == lp.Header {
					lp.Latches = append(lp.Latches, n)
				}
			}
		}
		sort.Strings(lp.Latches)
		var pre []string
		for _, pr := range c.pred[lp.Header] {
			if !lp.Blocks[pr] {
				pre = append(pre, pr)
			}
		}
		if len(pre) == 1 {
			lp.Preheader = pre[0]
		}
		lp.Ind = findInduction(p, lp, dom)
	}
}

// findInduction detects variables assigned exactly once in the loop, in
// the form v = v + const, where the assigning block dominates every
// latch (so each iteration performs exactly one increment).
func findInduction(p *Program, lp *Loop, dom map[string]map[string]bool) []Induction {
	type site struct {
		block string
		delta int
	}
	sites := map[string][]site{}
	for n := range lp.Blocks {
		for _, instr := range p.Block(n).Instrs {
			if instr.Kind == OpAssignAdd && instr.Dst == instr.Src && instr.C != 0 {
				sites[instr.Dst] = append(sites[instr.Dst], site{n, instr.C})
			} else if instr.Kind == OpAssignConst || instr.Kind == OpAssign || instr.Kind == OpAssignAdd {
				sites[instr.Dst] = append(sites[instr.Dst], site{n, 0})
			}
		}
	}
	var out []Induction
	for v, ss := range sites {
		if len(ss) != 1 || ss[0].delta == 0 {
			continue
		}
		ok := true
		for _, l := range lp.Latches {
			if !dom[l][ss[0].block] {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, Induction{Var: v, Delta: ss[0].delta})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Var < out[j].Var })
	return out
}

// dominators computes dom[n] = set of blocks dominating n.
func dominators(p *Program) map[string]map[string]bool {
	c := buildCFG(p)
	all := map[string]bool{}
	for _, b := range p.Blocks {
		all[b.Name] = true
	}
	dom := map[string]map[string]bool{}
	for _, b := range p.Blocks {
		dom[b.Name] = map[string]bool{}
		for n := range all {
			dom[b.Name][n] = true
		}
	}
	dom[p.Entry] = map[string]bool{p.Entry: true}
	changed := true
	for changed {
		changed = false
		for _, b := range p.Blocks {
			if b.Name == p.Entry {
				continue
			}
			nd := map[string]bool{}
			for n := range all {
				nd[n] = true
			}
			for _, pr := range c.pred[b.Name] {
				for n := range nd {
					if !dom[pr][n] {
						delete(nd, n)
					}
				}
			}
			nd[b.Name] = true
			if !sameSet(nd, dom[b.Name]) {
				dom[b.Name] = nd
				changed = true
			}
		}
	}
	return dom
}

func sameSet(a, b map[string]bool) bool {
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

// tarjan returns the SCCs of the reachable graph.
func tarjan(p *Program, c cfg) [][]string {
	index := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	var sccs [][]string
	counter := 0
	var strongconnect func(v string)
	strongconnect = func(v string) {
		index[v] = counter
		low[v] = counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range c.succ[v] {
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
			var scc []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			sort.Strings(scc)
			sccs = append(sccs, scc)
		}
	}
	strongconnect(p.Entry)
	return sccs
}
