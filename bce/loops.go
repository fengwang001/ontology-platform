package bce

// blockSet is a set of block indices.
type blockSet struct{ m map[int]bool }

func newBlockSet() *blockSet { return &blockSet{m: map[int]bool{}} }

func (s *blockSet) Add(i int)           { s.m[i] = true }
func (s *blockSet) Contains(i int) bool { return s.m[i] }
func (s *blockSet) Len() int            { return len(s.m) }

func (s *blockSet) clone() *blockSet {
	n := newBlockSet()
	for k := range s.m {
		n.m[k] = true
	}
	return n
}

// intersect keeps only elements present in both sets.
func (s *blockSet) intersect(o *blockSet) {
	for k := range s.m {
		if !o.m[k] {
			delete(s.m, k)
		}
	}
}

func (s *blockSet) equal(o *blockSet) bool {
	if len(s.m) != len(o.m) {
		return false
	}
	for k := range s.m {
		if !o.m[k] {
			return false
		}
	}
	return true
}

// members returns the set elements in ascending, deterministic order.
func (s *blockSet) members() []int {
	out := make([]int, 0, len(s.m))
	for k := range s.m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// dominators computes, for every block, the set of blocks that dominate it.
func dominators(c *cfg, entry int) []*blockSet {
	n := len(c.prog.Blocks)
	doms := make([]*blockSet, n)
	all := newBlockSet()
	for i := 0; i < n; i++ {
		all.Add(i)
		doms[i] = all.clone()
	}
	doms[entry] = newBlockSet()
	doms[entry].Add(entry)
	changed := true
	for changed {
		changed = false
		for b := 0; b < n; b++ {
			if b == entry {
				continue
			}
			var acc *blockSet
			for _, p := range c.preds[b] {
				if acc == nil {
					acc = doms[p].clone()
				} else {
					acc.intersect(doms[p])
				}
			}
			if acc == nil {
				acc = newBlockSet()
			}
			acc.Add(b)
			if !acc.equal(doms[b]) {
				doms[b] = acc
				changed = true
			}
		}
	}
	return doms
}

// naturalLoop returns the blocks of the natural loop whose back edge is
// latch -> header: the header plus every block that can reach the latch
// without passing through the header.
func naturalLoop(c *cfg, latch, header int) []int {
	loop := newBlockSet()
	loop.Add(header)
	var stack []int
	if latch != header {
		loop.Add(latch)
		stack = append(stack, latch)
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, p := range c.preds[cur] {
			if !loop.Contains(p) {
				loop.Add(p)
				stack = append(stack, p)
			}
		}
	}
	return loop.members()
}

// loopInfo describes one natural loop.
type loopInfo struct {
	header int
	latch  int // single back-edge source; -1 when not unique
	blocks *blockSet
	ind    *induction // nil when no provable induction variable exists
}

// induction describes a loop variable incremented by a fixed positive
// constant exactly once per iteration, plus the facts needed to bound it.
type induction struct {
	v           string
	step        int64
	assignBlock int
	outside     *factSet // facts at the header arriving from outside the loop
	guards      []indGuard
}

// indGuard is a comparison v op bound that holds on the loop's stay edge.
type indGuard struct {
	op     CmpOp // normalized: induction variable on the left
	bound  Opnd
	block  int
	origin string
}

// findLoops computes the natural loops of the program and, for each loop
// with a unique latch, its induction variable (if any). out[b] must hold
// the dataflow facts at the end of block b.
func findLoops(c *cfg, doms []*blockSet, out []*factSet, rel map[string]bool, stats *Stats) []*loopInfo {
	var loops []*loopInfo
	n := len(c.prog.Blocks)
	for b := 0; b < n; b++ {
		for _, s := range c.succs[b] {
			if !doms[b].Contains(s) {
				continue // not a back edge
			}
			members := naturalLoop(c, b, s)
			li := &loopInfo{header: s, latch: b, blocks: newBlockSet()}
			for _, m := range members {
				li.blocks.Add(m)
			}
			loops = append(loops, li)
		}
	}
	// Merge loops that share a header (multiple back edges): no unique
	// latch, so no induction reasoning for them.
	for i, li := range loops {
		for j := i + 1; j < len(loops); j++ {
			if loops[j].header == li.header {
				li.latch = -1
				loops[j].latch = -1
			}
		}
	}
	for _, li := range loops {
		if li.latch >= 0 {
			li.ind = detectInduction(c, li, out, rel, stats)
		}
	}
	return loops
}

// loopOf returns the innermost recorded loop containing block b, or nil.
func loopOf(loops []*loopInfo, b int) *loopInfo {
	var best *loopInfo
	for _, l := range loops {
		if l.blocks.Contains(b) && (best == nil || l.blocks.Len() < best.blocks.Len()) {
			best = l
		}
	}
	return best
}

// detectInduction finds a variable assigned exactly once in the loop as
// v = v + k (k a nonzero constant) and collects the loop's exit guards.
func detectInduction(c *cfg, l *loopInfo, out []*factSet, rel map[string]bool, stats *Stats) *induction {
	// Facts arriving at the header from outside the loop.
	outside := newFactSet()
	first := true
	for _, p := range c.preds[l.header] {
		if l.blocks.Contains(p) {
			continue
		}
		edge := applyEdge(c, p, l.header, out[p], rel, stats)
		if first {
			outside = edge
			first = false
		} else {
			outside.meet(edge, stats)
		}
	}

	// Candidate: scalar assigned exactly once inside the loop as v = v + k.
	assignCount := map[string]int{}
	type site struct {
		v     string
		step  int64
		block int
	}
	var sites []site
	for _, m := range l.blocks.members() {
		for _, ins := range c.prog.Blocks[m].Instrs {
			switch t := ins.(type) {
			case Const:
				assignCount[t.Dst]++
			case Input:
				assignCount[t.Dst]++
			case Copy:
				assignCount[t.Dst]++
			case LenOf:
				assignCount[t.Dst]++
			case Bin:
				assignCount[t.Dst]++
				if t.Op == Add {
					step, ok := int64(0), false
					if !t.A.IsConst && t.A.Var == t.Dst && t.B.IsConst {
						step, ok = t.B.Const, true
					}
					if !t.B.IsConst && t.B.Var == t.Dst && t.A.IsConst {
						step, ok = t.A.Const, true
					}
					if ok && step != 0 {
						sites = append(sites, site{v: t.Dst, step: step, block: m})
					}
				}
			}
		}
	}
	var ind *induction
	for _, s := range sites {
		if assignCount[s.v] == 1 {
			ind = &induction{v: s.v, step: s.step, assignBlock: s.block, outside: outside}
			break // first candidate in deterministic block order
		}
	}
	if ind == nil {
		return nil
	}

	// Guards: exit branches inside the loop comparing the induction
	// variable; record the comparison that holds on the stay edge.
	for _, m := range l.blocks.members() {
		blk := c.prog.Blocks[m]
		br, ok := blk.Term().(Br)
		if !ok {
			continue
		}
		op, bound, ok := normalizeVarLeft(br.A, br.Op, br.B, ind.v)
		if !ok {
			continue
		}
		thenIn := l.blocks.Contains(c.index[br.Then])
		elseIn := l.blocks.Contains(c.index[br.Else])
		switch {
		case thenIn && !elseIn:
			ind.guards = append(ind.guards, indGuard{op: op, bound: bound, block: m, origin: blk.ID + ":true"})
		case elseIn && !thenIn:
			ind.guards = append(ind.guards, indGuard{op: op.Negate(), bound: bound, block: m, origin: blk.ID + ":false"})
		}
	}
	return ind
}

// normalizeVarLeft rewrites the comparison so that variable v is on the
// left; ok is false when neither side is v.
func normalizeVarLeft(a Opnd, op CmpOp, b Opnd, v string) (CmpOp, Opnd, bool) {
	if !a.IsConst && a.Var == v {
		return op, b, true
	}
	if !b.IsConst && b.Var == v {
		return op.Flip(), a, true
	}
	return op, b, false
}
