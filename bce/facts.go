package bce

import "fmt"

// factEntry is a constant-valued fact plus where it was established.
type factEntry struct {
	val    int64
	origin string
}

// cmpEntry is a path comparison fact: a op b holds, established at origin.
type cmpEntry struct {
	op     CmpOp
	a, b   Opnd
	origin string
}

// ckKey identifies a passed-check fact: Idx is in bounds for Arr.
type ckKey struct{ arr, idx string }

// factSet is the set of facts that provably hold at one program point.
// Only facts about relevant variables are ever stored, so the size of a
// factSet — and therefore the cost of meet — is independent of the number
// of unrelated variables in the program.
type factSet struct {
	consts  map[string]factEntry // scalar var -> constant value
	lens    map[string]factEntry // array var -> constant length
	cmps    map[string]cmpEntry  // key -> comparison fact
	checked map[ckKey]string     // (arr, idx) -> origin of the passed check
}

func newFactSet() *factSet {
	return &factSet{
		consts:  map[string]factEntry{},
		lens:    map[string]factEntry{},
		cmps:    map[string]cmpEntry{},
		checked: map[ckKey]string{},
	}
}

func (fs *factSet) clone() *factSet {
	n := newFactSet()
	for k, v := range fs.consts {
		n.consts[k] = v
	}
	for k, v := range fs.lens {
		n.lens[k] = v
	}
	for k, v := range fs.cmps {
		n.cmps[k] = v
	}
	for k, v := range fs.checked {
		n.checked[k] = v
	}
	return n
}

func opndStr(o Opnd) string {
	if o.IsConst {
		return fmt.Sprintf("#%d", o.Const)
	}
	return o.Var
}

func cmpKey(op CmpOp, a, b Opnd) string {
	return opndStr(a) + op.String() + opndStr(b)
}

// meet intersects src into dst (facts must hold on both sides of a join).
// Each map is intersected by iterating the smaller side, so the cost is
// proportional to the number of stored (relevant) facts only.
func (dst *factSet) meet(src *factSet, stats *Stats) {
	meetEntry := func(dstM, srcM map[string]factEntry) {
		for k, v := range dstM {
			if stats != nil {
				stats.MeetOps++
			}
			if sv, ok := srcM[k]; !ok || sv.val != v.val {
				delete(dstM, k)
			}
		}
	}
	meetEntry(dst.consts, src.consts)
	meetEntry(dst.lens, src.lens)
	for k := range dst.cmps {
		if stats != nil {
			stats.MeetOps++
		}
		if _, ok := src.cmps[k]; !ok {
			delete(dst.cmps, k)
		}
	}
	for k := range dst.checked {
		if stats != nil {
			stats.MeetOps++
		}
		if _, ok := src.checked[k]; !ok {
			delete(dst.checked, k)
		}
	}
}

// resolve maps an operand to a constant using known constant facts.
func (fs *factSet) resolve(o Opnd, stats *Stats) (int64, bool) {
	if o.IsConst {
		return o.Const, true
	}
	if stats != nil {
		stats.ProveOps++
	}
	e, ok := fs.consts[o.Var]
	return e.val, ok
}

// killScalar invalidates every fact that mentions scalar variable v.
func (fs *factSet) killScalar(v string) {
	delete(fs.consts, v)
	for k, e := range fs.cmps {
		if (!e.a.IsConst && e.a.Var == v) || (!e.b.IsConst && e.b.Var == v) {
			delete(fs.cmps, k)
		}
	}
	for k := range fs.checked {
		if k.idx == v {
			delete(fs.checked, k)
		}
	}
}

// killArray invalidates every length fact and passed-check fact of array a.
func (fs *factSet) killArray(a string) {
	delete(fs.lens, a)
	for k := range fs.checked {
		if k.arr == a {
			delete(fs.checked, k)
		}
	}
}

// addCond records a branch condition that holds on the current edge.
func (fs *factSet) addCond(a Opnd, op CmpOp, b Opnd, origin string, rel map[string]bool) {
	if a.IsConst && b.IsConst {
		return
	}
	// x == const gives a constant fact on this path.
	if op == EQ {
		if !a.IsConst && b.IsConst && rel[a.Var] {
			fs.consts[a.Var] = factEntry{b.Const, origin}
			return
		}
		if !b.IsConst && a.IsConst && rel[b.Var] {
			fs.consts[b.Var] = factEntry{a.Const, origin}
			return
		}
	}
	if op == NE {
		return // no usable bound from disequality
	}
	// Normalize: a single variable goes on the left.
	if a.IsConst && !b.IsConst {
		a, b, op = b, a, op.Flip()
	}
	if !rel[a.Var] && (b.IsConst || !rel[b.Var]) {
		return
	}
	fs.cmps[cmpKey(op, a, b)] = cmpEntry{op: op, a: a, b: b, origin: origin}
}

// transferInstr applies one non-terminator instruction to fs in place.
// origin identifies the instruction, e.g. "b0#3".
func (fs *factSet) transferInstr(ins Instr, origin string, rel map[string]bool, stats *Stats) {
	switch t := ins.(type) {
	case Const:
		fs.killScalar(t.Dst)
		if rel[t.Dst] {
			fs.consts[t.Dst] = factEntry{t.Val, origin}
		}
	case Input:
		fs.killScalar(t.Dst)
	case Bin:
		fs.killScalar(t.Dst)
		if rel[t.Dst] {
			av, aok := fs.resolve(t.A, stats)
			bv, bok := fs.resolve(t.B, stats)
			if aok && bok {
				switch t.Op {
				case Add:
					fs.consts[t.Dst] = factEntry{av + bv, origin}
				case Sub:
					fs.consts[t.Dst] = factEntry{av - bv, origin}
				}
			}
		}
	case Copy:
		fs.killScalar(t.Dst)
		if rel[t.Dst] {
			if e, ok := fs.consts[t.Src]; ok {
				fs.consts[t.Dst] = factEntry{e.val, origin}
			}
		}
	case NewArray:
		fs.killArray(t.Dst)
		if rel[t.Dst] {
			if sv, ok := fs.resolve(t.Size, stats); ok {
				fs.lens[t.Dst] = factEntry{sv, origin}
			}
		}
	case ArrCopy:
		fs.killArray(t.Dst)
		if rel[t.Dst] {
			if e, ok := fs.lens[t.Src]; ok {
				fs.lens[t.Dst] = factEntry{e.val, origin}
			}
		}
	case LenOf:
		fs.killScalar(t.Dst)
		if rel[t.Dst] {
			if e, ok := fs.lens[t.Arr]; ok {
				fs.consts[t.Dst] = factEntry{e.val, origin}
			}
		}
	case Check:
		fs.checked[ckKey{t.Arr, t.Idx}] = origin
	}
}

// transferBlock applies every non-terminator instruction of the block.
func transferBlock(in *factSet, blk *Block, rel map[string]bool, stats *Stats) *factSet {
	fs := in.clone()
	for pos, ins := range blk.Instrs {
		if isTerminator(ins) {
			continue
		}
		fs.transferInstr(ins, fmt.Sprintf("%s#%d", blk.ID, pos), rel, stats)
	}
	return fs
}

// applyEdge returns the facts that hold at the head of block `to` when
// arriving from block `from`, adding the branch condition of the edge.
func applyEdge(c *cfg, from, to int, out *factSet, rel map[string]bool, stats *Stats) *factSet {
	blk := c.prog.Blocks[from]
	br, ok := blk.Term().(Br)
	fs := out.clone()
	if !ok {
		return fs
	}
	origin := blk.ID
	if to == c.index[br.Then] {
		fs.addCond(br.A, br.Op, br.B, origin+":true", rel)
	} else if to == c.index[br.Else] {
		fs.addCond(br.A, br.Op.Negate(), br.B, origin+":false", rel)
	}
	return fs
}

// computeRelevance finds every variable whose facts may influence a check
// decision: check operands plus the transitive def-use slice around them.
func computeRelevance(c *cfg) map[string]bool {
	rel := map[string]bool{}
	for _, site := range c.checks {
		rel[site.chk.Arr] = true
		rel[site.chk.Idx] = true
	}
	markOpnd := func(o Opnd) {
		if !o.IsConst {
			rel[o.Var] = true
		}
	}
	changed := true
	for changed {
		changed = false
		add := func(v string) {
			if !rel[v] {
				rel[v] = true
				changed = true
			}
		}
		for _, b := range c.prog.Blocks {
			for _, ins := range b.Instrs {
				switch t := ins.(type) {
				case Bin:
					if rel[t.Dst] {
						markOpnd(t.A)
						markOpnd(t.B)
					}
				case Copy:
					if rel[t.Dst] {
						add(t.Src)
					}
					if rel[t.Src] {
						add(t.Dst)
					}
				case NewArray:
					if rel[t.Dst] {
						markOpnd(t.Size)
					}
				case ArrCopy:
					if rel[t.Dst] {
						add(t.Src)
					}
					if rel[t.Src] {
						add(t.Dst)
					}
				case LenOf:
					if rel[t.Dst] {
						add(t.Arr)
					}
					if rel[t.Arr] {
						add(t.Dst)
					}
				case Br:
					if !t.A.IsConst && rel[t.A.Var] {
						markOpnd(t.B)
					}
					if !t.B.IsConst && rel[t.B.Var] {
						markOpnd(t.A)
					}
				}
			}
		}
	}
	return rel
}

// computeFacts runs the forward dataflow analysis to a fixpoint and then
// snapshots the fact set at each check. Block and predecessor iteration
// orders are fixed by the program, so the result is deterministic. Only
// meet operations are counted here; proof operations are counted by the
// prover, keeping the decision-cost metric independent of program length.
func computeFacts(c *cfg, rel map[string]bool, stats *Stats) (in, out []*factSet, pre map[string]*factSet) {
	n := len(c.prog.Blocks)
	in = make([]*factSet, n)
	out = make([]*factSet, n)
	// Must-analysis: meet is intersection, so the neutral element (top) is
	// "all facts". nil plays the role of top: it is skipped in meets and
	// only blocks reachable from the entry ever become non-nil. Iterating
	// from top yields the greatest fixpoint, i.e. exactly the facts that
	// hold on every path.
	entry := c.index[c.prog.Entry]
	changed := true
	for changed {
		changed = false
		for b := 0; b < n; b++ {
			if b == entry {
				in[b] = newFactSet()
				newOut := transferBlock(in[b], c.prog.Blocks[b], rel, stats)
				if out[b] == nil || !newOut.equal(out[b]) {
					out[b] = newOut
					changed = true
				}
				continue
			}
			var acc *factSet
			for _, p := range c.preds[b] {
				if out[p] == nil {
					continue // top: identity for intersection
				}
				edge := applyEdge(c, p, b, out[p], rel, stats)
				if acc == nil {
					acc = edge
				} else {
					acc.meet(edge, stats)
				}
			}
			if acc == nil {
				continue // no predecessor computed yet: still top
			}
			in[b] = acc
			newOut := transferBlock(acc, c.prog.Blocks[b], rel, nil)
			if out[b] == nil || !newOut.equal(out[b]) {
				out[b] = newOut
				changed = true
			}
		}
	}
	// Snapshot the state immediately before each check.
	pre = map[string]*factSet{}
	for _, site := range c.checks {
		fs := in[site.block].clone()
		blk := c.prog.Blocks[site.block]
		for pos := 0; pos < site.pos; pos++ {
			fs.transferInstr(blk.Instrs[pos], fmt.Sprintf("%s#%d", blk.ID, pos), rel, nil)
		}
		pre[site.chk.ID] = fs
	}
	return in, out, pre
}

func (fs *factSet) equal(o *factSet) bool {
	eqEntry := func(a, b map[string]factEntry) bool {
		if len(a) != len(b) {
			return false
		}
		for k, v := range a {
			if bv, ok := b[k]; !ok || bv.val != v.val {
				return false
			}
		}
		return true
	}
	if !eqEntry(fs.consts, o.consts) || !eqEntry(fs.lens, o.lens) {
		return false
	}
	if len(fs.cmps) != len(o.cmps) || len(fs.checked) != len(o.checked) {
		return false
	}
	for k := range fs.cmps {
		if _, ok := o.cmps[k]; !ok {
			return false
		}
	}
	for k := range fs.checked {
		if _, ok := o.checked[k]; !ok {
			return false
		}
	}
	return true
}
