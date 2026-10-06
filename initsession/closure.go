package initsession

// Closure representation
//
// Each named variable gets a dense int32 id assigned by (unit source
// order, left-hand-side position). A function's transitive closure is
// the set of variable ids reached through its body: a direct variable
// reference adds that variable; a direct function reference adds the
// referenced function's closure; references to predeclared names add
// nothing. By the time closures are built, undeclared references have
// already been rejected.
//
// Functions may be mutually recursive. The function call graph is
// decomposed into strongly connected components (Tarjan). All
// functions in one SCC share one closure: the union of direct variable
// references of every member plus the unions of successor SCC
// closures. SCCs are merged in reverse topological order using
// small-to-large set union, so each stored variable id moves O(log F)
// times and every function body is walked exactly once. A function
// referenced by thousands of units is therefore expanded exactly
// once; its closure is shared by every consumer.

type closureState struct {
	snap *snapshot
	st   *Stats

	varID    map[string]int32
	varName  []string
	varOwner []int

	fnDirectVars [][]int32
	fnCallEdges  [][]int

	sccOf      []int
	sccClosure [][]int32
}

func (c *closureState) init() {
	snap := c.snap
	c.varID = make(map[string]int32)
	for _, u := range snap.units {
		for _, v := range u.vars {
			if IsBlank(v) {
				continue
			}
			id := int32(len(c.varName))
			c.varID[v] = id
			c.varName = append(c.varName, v)
			c.varOwner = append(c.varOwner, u.index)
		}
	}

	f := len(snap.functions)
	c.fnDirectVars = make([][]int32, f)
	c.fnCallEdges = make([][]int, f)
	for _, fn := range snap.functions {
		var vars []int32
		var calls []int
		for _, ref := range fn.refs {
			c.st.RefEdgesTraversed++
			info := snap.names[ref]
			switch info.kind {
			case kindVariable:
				vars = append(vars, c.varID[ref])
			case kindFunction:
				calls = append(calls, info.funcIndex)
			}
		}
		c.fnDirectVars[fn.index] = uniqueSortedInt32(vars)
		c.fnCallEdges[fn.index] = uniqueSortedInt(calls)
	}
}

func (c *closureState) build() {
	c.init()
	comps := tarjan(c.fnCallEdges)
	c.sccOf = comps.sccOf

	nc := comps.count
	c.sccClosure = make([][]int32, nc)

	// comps.order lists SCCs in reverse topological order: successors
	// come first, so closures are ready when a component is processed.
	for ci, members := range comps.order {
		var set []int32
		for _, f := range members {
			c.st.FunctionClosuresComputed++
			set = unionInt32(set, c.fnDirectVars[f])
			for _, g := range c.fnCallEdges[f] {
				c.st.FunctionRefExpansions++
				if comps.sccOf[g] != ci {
					c.st.FunctionCacheHits++
					set = unionInt32(set, c.sccClosure[comps.sccOf[g]])
				}
			}
		}
		c.sccClosure[ci] = set
	}
}

// unitClosure computes the transitive variable dependencies of one
// unit. The current unit's own variables are excluded from deps (the
// reported dependency set never contains them), but depsOn retains a
// self edge so a self-referential unit is detected as a cycle during
// scheduling.
func (c *closureState) unitClosure(u *unit) (deps []int32, depsOn map[int]struct{}) {
	depsOn = make(map[int]struct{})
	var set []int32
	for _, ref := range u.refs {
		c.st.RefEdgesTraversed++
		info := c.snap.names[ref]
		switch info.kind {
		case kindVariable:
			id := c.varID[ref]
			if c.varOwner[id] != u.index {
				set = append(set, id)
			}
			depsOn[c.varOwner[id]] = struct{}{}
		case kindFunction:
			c.st.FunctionRefExpansions++
			c.st.FunctionCacheHits++
			closure := c.sccClosure[c.sccOf[info.funcIndex]]
			for _, id := range closure {
				if c.varOwner[id] != u.index {
					set = append(set, id)
				}
				depsOn[c.varOwner[id]] = struct{}{}
			}
		}
	}
	return uniqueSortedInt32(set), depsOn
}

func uniqueSortedInt32(x []int32) []int32 {
	if len(x) < 2 {
		return x
	}
	sortInt32s(x)
	out := x[:1]
	for _, v := range x[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

func uniqueSortedInt(x []int) []int {
	if len(x) < 2 {
		return x
	}
	sortInts(x)
	out := x[:1]
	for _, v := range x[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

// unionInt32 merges two sorted-unique slices using small-to-large
// insertion: ids in the smaller slice are binary-searched into the
// larger one. Each id is reinserted O(log total) times overall.
//
// Neither input slice is mutated: closures are shared between SCCs and
// between consuming units, so the first write copies the destination's
// backing array (copy-on-write).
func unionInt32(a, b []int32) []int32 {
	if len(a) < len(b) {
		a, b = b, a
	}
	if len(b) == 0 {
		return a
	}
	merged := make([]int32, 0, len(a)+len(b))
	ia, ib := 0, 0
	for ia < len(a) || ib < len(b) {
		switch {
		case ib == len(b) || (ia < len(a) && a[ia] < b[ib]):
			merged = append(merged, a[ia])
			ia++
		case ia == len(a) || b[ib] < a[ia]:
			merged = append(merged, b[ib])
			ib++
		default:
			merged = append(merged, a[ia])
			ia++
			ib++
		}
	}
	return merged
}
