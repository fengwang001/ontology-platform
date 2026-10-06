package bce

import (
	"fmt"
	"sort"
)

// sortedCmpKeys returns the comparison fact keys in deterministic order.
func sortedCmpKeys(fs *factSet) []string {
	keys := make([]string, 0, len(fs.cmps))
	for k := range fs.cmps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortedCheckedKeys returns the passed-check fact keys in deterministic
// order.
func sortedCheckedKeys(fs *factSet) []ckKey {
	keys := make([]ckKey, 0, len(fs.checked))
	for k := range fs.checked {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].arr != keys[j].arr {
			return keys[i].arr < keys[j].arr
		}
		return keys[i].idx < keys[j].idx
	})
	return keys
}

// prover decides every check from the computed facts. All lookups are
// keyed by the check's own variables, so the cost of one decision depends
// only on the number of relevant facts, not on the program size.
type prover struct {
	c     *cfg
	doms  []*blockSet
	loops []*loopInfo
	in    []*factSet
	out   []*factSet
	pre   map[string]*factSet
	rel   map[string]bool
	stats *Stats
}

type sideProof struct {
	ok       bool
	evidence []string
}

func (p *prover) count() {
	if p.stats != nil {
		p.stats.ProveOps++
	}
}

// decide produces the unique decision for one check.
func (p *prover) decide(site checkSite) Decision {
	chk := site.chk
	fs := p.pre[chk.ID]
	lower := p.proveLower(chk, site, fs)
	upper := p.proveUpper(chk, site, fs)
	d := Decision{
		CheckID: chk.ID,
		Removed: lower.ok && upper.ok,
		Lower:   Side{Proven: lower.ok, Evidence: lower.evidence},
		Upper:   Side{Proven: upper.ok, Evidence: upper.evidence},
	}
	if !lower.ok {
		d.Lower.Cause = p.diagnose(site, fs, false)
	}
	if !upper.ok {
		d.Upper.Cause = p.diagnose(site, fs, true)
	}
	return d
}

// proveLower proves Idx >= 0 at the check.
func (p *prover) proveLower(chk *Check, site checkSite, fs *factSet) sideProof {
	p.count()
	if e, ok := fs.consts[chk.Idx]; ok && e.val >= 0 {
		return sideProof{true, []string{fmt.Sprintf("const(%s)=%d@%s", chk.Idx, e.val, e.origin)}}
	}
	// Path comparison facts: idx >= k with k >= 0, or idx > k with k >= -1.
	for _, key := range sortedCmpKeys(fs) {
		p.count()
		e := fs.cmps[key]
		op, bound, ok := varSide(e, chk.Idx)
		if !ok {
			continue
		}
		k, ok := fs.resolve(bound, p.stats)
		if !ok {
			continue
		}
		if (op == GE && k >= 0) || (op == GT && k >= -1) {
			return sideProof{true, []string{fmt.Sprintf("cond(%s %s %s)@%s",
				chk.Idx, op, opndStr(bound), e.origin)}}
		}
	}
	// A previously passed check on the same index proves idx >= 0.
	for _, k := range sortedCheckedKeys(fs) {
		p.count()
		origin := fs.checked[k]
		if k.idx == chk.Idx {
			return sideProof{true, []string{fmt.Sprintf("passed-check(%s,%s)@%s", k.arr, k.idx, origin)}}
		}
	}
	// Loop induction: idx starts provably >= 0 and increases by a fixed
	// positive step, so it stays >= 0 for the whole loop.
	if pr, ok := p.inductionLower(chk, site); ok {
		return pr
	}
	return sideProof{}
}

// proveUpper proves Idx < len(Arr) at the check.
func (p *prover) proveUpper(chk *Check, site checkSite, fs *factSet) sideProof {
	p.count()
	if origin, ok := fs.checked[ckKey{chk.Arr, chk.Idx}]; ok {
		return sideProof{true, []string{fmt.Sprintf("passed-check(%s,%s)@%s", chk.Arr, chk.Idx, origin)}}
	}
	L, lok := fs.lens[chk.Arr]
	if lok {
		p.count()
		if e, ok := fs.consts[chk.Idx]; ok && e.val < L.val {
			return sideProof{true, []string{
				fmt.Sprintf("const(%s)=%d@%s", chk.Idx, e.val, e.origin),
				fmt.Sprintf("len(%s)=%d@%s", chk.Arr, L.val, L.origin),
			}}
		}
		// Path comparison facts give idx < bound or idx <= bound; the
		// strictness of the comparison is matched exactly against L.
		for _, key := range sortedCmpKeys(fs) {
			p.count()
			e := fs.cmps[key]
			op, bound, ok := varSide(e, chk.Idx)
			if !ok {
				continue
			}
			k, ok := fs.resolve(bound, p.stats)
			if !ok {
				continue
			}
			if (op == LT && k <= L.val) || (op == LE && k < L.val) || (op == EQ && k < L.val) {
				return sideProof{true, []string{
					fmt.Sprintf("cond(%s %s %s)@%s", chk.Idx, op, opndStr(bound), e.origin),
					fmt.Sprintf("len(%s)=%d@%s", chk.Arr, L.val, L.origin),
				}}
			}
		}
	}
	// Loop induction: the exit guard bounds the index for the whole loop.
	if pr, ok := p.inductionUpper(chk, site, fs); ok {
		return pr
	}
	return sideProof{}
}

// varSide reads a comparison fact as "v op bound"; ok is false when the
// fact does not involve v.
func varSide(e cmpEntry, v string) (CmpOp, Opnd, bool) {
	if !e.a.IsConst && e.a.Var == v {
		return e.op, e.b, true
	}
	if !e.b.IsConst && e.b.Var == v {
		return e.op.Flip(), e.a, true
	}
	return e.op, e.b, false
}

// inductionLower proves idx >= 0 for an induction variable with a
// provably non-negative start and a positive fixed step.
func (p *prover) inductionLower(chk *Check, site checkSite) (sideProof, bool) {
	loop := loopOf(p.loops, site.block)
	if loop == nil || loop.ind == nil || loop.ind.v != chk.Idx || loop.ind.step <= 0 {
		return sideProof{}, false
	}
	ind := loop.ind
	p.count()
	init, ok := ind.outside.consts[ind.v]
	if !ok || init.val < 0 {
		return sideProof{}, false
	}
	return sideProof{true, []string{fmt.Sprintf("induction(%s,step=%d,init=%d>=0@%s)",
		ind.v, ind.step, init.val, init.origin)}}, true
}

// inductionUpper proves idx < len(arr) for the whole loop from the exit
// guard. It applies when the increment dominates the guard (do-while
// shape), the guard dominates the latch, the check observes the value the
// header sees, and the start value already satisfies the guard. The
// strictness of the guard comparison is matched exactly against the
// length: "idx < B" needs B <= L, "idx <= B" needs B < L.
func (p *prover) inductionUpper(chk *Check, site checkSite, fs *factSet) (sideProof, bool) {
	loop := loopOf(p.loops, site.block)
	if loop == nil || loop.ind == nil || loop.ind.v != chk.Idx || loop.ind.step <= 0 {
		return sideProof{}, false
	}
	ind := loop.ind
	L, lok := fs.lens[chk.Arr]
	if !lok {
		return sideProof{}, false
	}
	// The check must observe the header value of idx: no path from the
	// header to the check may pass through the increment.
	if p.reachesThrough(site.block, ind, site) {
		return sideProof{}, false
	}
	for _, g := range ind.guards {
		if g.op != LT && g.op != LE {
			continue
		}
		// The increment dominates the guard (guard sees the next value)
		// and the guard dominates the latch (evaluated every iteration).
		if !p.doms[g.block].Contains(ind.assignBlock) || !p.doms[loop.latch].Contains(g.block) {
			continue
		}
		p.count()
		bound, ok := ind.outside.resolve(g.bound, p.stats)
		if !ok {
			continue
		}
		init, ok := ind.outside.consts[ind.v]
		if !ok {
			continue
		}
		// The start value must itself satisfy the guard comparison.
		if g.op == LT && !(init.val < bound) {
			continue
		}
		if g.op == LE && !(init.val <= bound) {
			continue
		}
		// Open/closed precision: idx < B needs B <= L; idx <= B needs B < L.
		if g.op == LT && bound > L.val {
			continue
		}
		if g.op == LE && bound >= L.val {
			continue
		}
		return sideProof{true, []string{
			fmt.Sprintf("induction(%s,step=%d,guard %s %s %d@%s)", ind.v, ind.step, ind.v, g.op, bound, g.origin),
			fmt.Sprintf("len(%s)=%d@%s", chk.Arr, L.val, L.origin),
		}}, true
	}
	return sideProof{}, false
}

// reachesThrough reports whether the check may observe the post-increment
// value of the induction variable: either the increment is earlier in the
// same block, or the increment's block can reach the check's block.
func (p *prover) reachesThrough(checkBlock int, ind *induction, site checkSite) bool {
	assignBlock := ind.assignBlock
	if checkBlock == assignBlock {
		blk := p.c.prog.Blocks[assignBlock]
		for pos, ins := range blk.Instrs {
			if b, ok := ins.(Bin); ok && b.Dst == ind.v && pos < site.pos {
				return true
			}
		}
		return false
	}
	// BFS from assignBlock: can it reach checkBlock at all?
	seen := map[int]bool{assignBlock: true}
	queue := []int{assignBlock}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, s := range p.c.succs[cur] {
			if s == checkBlock {
				return true
			}
			if !seen[s] {
				seen[s] = true
				queue = append(queue, s)
			}
		}
	}
	return false
}

// diagnose attributes the reason a bound could not be proven, in a fixed
// priority order: array reassignment, loop assignment, join-lost fact,
// and finally reasoning outside the four allowed fact kinds.
func (p *prover) diagnose(site checkSite, fs *factSet, upper bool) Cause {
	chk := site.chk
	if p.arrReassigned(chk.Arr, site) {
		return CauseArrReassigned
	}
	if p.killedInLoop(chk, site) {
		return CauseLoopKilled
	}
	if p.joinLost(chk, site, upper) {
		return CauseJoinLost
	}
	return CauseOutOfScope
}

// arrReassigned reports whether the array variable is assigned again
// (a second allocation or a copy of another array) on some path that
// reaches the check.
func (p *prover) arrReassigned(arr string, site checkSite) bool {
	allocs := 0
	reassigned := false
	visit := func(bi int, upto int) {
		for pos, ins := range p.c.prog.Blocks[bi].Instrs {
			if upto >= 0 && pos > upto {
				break
			}
			switch t := ins.(type) {
			case NewArray:
				if t.Dst == arr {
					allocs++
				}
			case ArrCopy:
				if t.Dst == arr {
					reassigned = true
				}
			}
		}
	}
	visit(site.block, site.pos)
	seen := map[int]bool{site.block: true}
	queue := []int{site.block}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, pr := range p.c.preds[cur] {
			if !seen[pr] {
				seen[pr] = true
				visit(pr, -1)
				queue = append(queue, pr)
			}
		}
	}
	return reassigned || allocs > 1
}

// killedInLoop reports whether the check's index or array is assigned
// inside a loop enclosing the check, invalidating facts at the loop head.
func (p *prover) killedInLoop(chk *Check, site checkSite) bool {
	loop := loopOf(p.loops, site.block)
	if loop == nil {
		return false
	}
	for _, m := range loop.blocks.members() {
		for _, ins := range p.c.prog.Blocks[m].Instrs {
			switch t := ins.(type) {
			case Const:
				if t.Dst == chk.Idx {
					return true
				}
			case Input:
				if t.Dst == chk.Idx {
					return true
				}
			case Bin:
				if t.Dst == chk.Idx {
					return true
				}
			case Copy:
				if t.Dst == chk.Idx {
					return true
				}
			case LenOf:
				if t.Dst == chk.Idx {
					return true
				}
			case NewArray:
				if t.Dst == chk.Arr {
					return true
				}
			case ArrCopy:
				if t.Dst == chk.Arr {
					return true
				}
			}
		}
	}
	return false
}

// joinLost reports whether a fact that could prove the bound holds on some
// incoming edge of the check's block but was dropped at the join.
func (p *prover) joinLost(chk *Check, site checkSite, upper bool) bool {
	head := p.in[site.block]
	hasCmp := func(fs *factSet) bool {
		for _, e := range fs.cmps {
			if _, _, ok := varSide(e, chk.Idx); ok {
				return true
			}
		}
		return false
	}
	lost := func(edge *factSet) bool {
		if _, ok := edge.consts[chk.Idx]; ok {
			if _, ok2 := head.consts[chk.Idx]; !ok2 {
				return true
			}
		}
		if _, ok := edge.checked[ckKey{chk.Arr, chk.Idx}]; ok {
			if _, ok2 := head.checked[ckKey{chk.Arr, chk.Idx}]; !ok2 {
				return true
			}
		}
		if hasCmp(edge) && !hasCmp(head) {
			return true
		}
		if upper {
			if _, ok := edge.lens[chk.Arr]; ok {
				if _, ok2 := head.lens[chk.Arr]; !ok2 {
					return true
				}
			}
		}
		return false
	}
	for _, pr := range p.c.preds[site.block] {
		edge := applyEdge(p.c, pr, site.block, p.out[pr], p.rel, nil)
		if lost(edge) {
			return true
		}
	}
	return false
}
