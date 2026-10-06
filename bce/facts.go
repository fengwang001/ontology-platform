package bce

import (
	"fmt"
	"sort"
)

// fkind classifies the four allowed fact classes.
type fkind int

const (
	fkConst fkind = iota // scalar variable has a constant value
	fkLen                // array variable has a constant length
	fkCmp                // comparison condition holds on this path
	fkCheck              // a bounds check has passed on this path
)

// Cmp is a normalized comparison: Left is always a variable; the right
// side is a constant or a variable.
type Cmp struct {
	Left   string
	Op     string
	RConst bool
	RVal   int
	RVar   string
}

func (c Cmp) key() string {
	r := c.RVar
	if c.RConst {
		r = fmt.Sprintf("%d", c.RVal)
	}
	return c.Left + c.Op + r
}

func (c Cmp) String() string {
	r := c.RVar
	if c.RConst {
		r = fmt.Sprintf("%d", c.RVal)
	}
	return c.Left + " " + c.Op + " " + r
}

// fact is one proven fact plus its source.
type fact struct {
	kind fkind
	src  string
	cval int     // fkConst, fkLen
	cmp  Cmp     // fkCmp
	arr  string  // fkCheck
	idx  Operand // fkCheck
}

func (f fact) key() string {
	switch f.kind {
	case fkConst:
		return "K/" + f.arr
	case fkLen:
		return "L/" + f.arr
	case fkCmp:
		return "C/" + f.cmp.key()
	case fkCheck:
		return "P/" + f.arr + "/" + f.idx.String()
	}
	return ""
}

func (f fact) String() string {
	switch f.kind {
	case fkConst:
		return fmt.Sprintf("%s = %d", f.arr, f.cval)
	case fkLen:
		return fmt.Sprintf("len(%s) = %d", f.arr, f.cval)
	case fkCmp:
		return f.cmp.String()
	case fkCheck:
		return fmt.Sprintf("passed(%s[%s])", f.arr, f.idx.String())
	}
	return ""
}

// vars returns the variable/array names this fact mentions.
func (f fact) vars() []string {
	switch f.kind {
	case fkConst, fkLen:
		return []string{f.arr}
	case fkCmp:
		if f.cmp.RConst {
			return []string{f.cmp.Left}
		}
		return []string{f.cmp.Left, f.cmp.RVar}
	case fkCheck:
		if f.idx.IsConst {
			return []string{f.arr}
		}
		return []string{f.arr, f.idx.Var}
	}
	return nil
}

func factEqual(a, b fact) bool {
	return a.kind == b.kind && a.cval == b.cval && a.cmp == b.cmp &&
		a.arr == b.arr && a.idx == b.idx
}

// lostCause explains why a fact no longer holds, for diagnostics.
type lostCause int

const (
	lcAssign lostCause = iota // overwritten by straight-line assignment
	lcJoin                    // lost at a path join
	lcLoop                    // killed by an assignment inside a loop
	lcArray                   // invalidated by array reassignment
)

func causePriority(c lostCause) int {
	switch c {
	case lcArray:
		return 3
	case lcLoop:
		return 2
	case lcJoin:
		return 1
	}
	return 0
}

type lost struct {
	f     fact
	cause lostCause
}

// FactSet is the set of facts that must hold at a program point.
// Maps are shared by pointer and cloned on write, so a join of two paths
// that share a map costs O(1) and a meet only scans the smaller side.
type FactSet struct {
	reachable bool
	facts     *map[string]fact
	byVar     *map[string][]string
	lost      *map[string]lost
	lostByVar *map[string][]string
}

func newFactSet(reachable bool) *FactSet {
	f := map[string]fact{}
	b := map[string][]string{}
	l := map[string]lost{}
	lb := map[string][]string{}
	return &FactSet{reachable: reachable, facts: &f, byVar: &b, lost: &l, lostByVar: &lb}
}

func cloneMap[K comparable, V any](m *map[K]V) *map[K]V {
	n := make(map[K]V, len(*m))
	for k, v := range *m {
		n[k] = v
	}
	return &n
}

func (fs *FactSet) addFact(f fact) {
	k := f.key()
	if old, ok := (*fs.facts)[k]; ok && factEqual(old, f) {
		return
	}
	fs.facts = cloneMap(fs.facts)
	(*fs.facts)[k] = f
	fs.byVar = cloneMap(fs.byVar)
	for _, v := range f.vars() {
		(*fs.byVar)[v] = append(append([]string(nil), (*fs.byVar)[v]...), k)
	}
}

func (fs *FactSet) removeFact(k string) {
	f, ok := (*fs.facts)[k]
	if !ok {
		return
	}
	fs.facts = cloneMap(fs.facts)
	delete(*fs.facts, k)
	fs.byVar = cloneMap(fs.byVar)
	for _, v := range f.vars() {
		var nk []string
		for _, kk := range (*fs.byVar)[v] {
			if kk != k {
				nk = append(nk, kk)
			}
		}
		(*fs.byVar)[v] = nk
	}
}

func (fs *FactSet) addLost(f fact, cause lostCause) {
	k := f.key()
	if old, ok := (*fs.lost)[k]; ok && causePriority(old.cause) >= causePriority(cause) {
		return
	}
	fs.lost = cloneMap(fs.lost)
	(*fs.lost)[k] = lost{f: f, cause: cause}
	fs.lostByVar = cloneMap(fs.lostByVar)
	for _, v := range f.vars() {
		(*fs.lostByVar)[v] = append(append([]string(nil), (*fs.lostByVar)[v]...), k)
	}
}

// killVar invalidates every fact mentioning scalar variable v.
func (fs *FactSet) killVar(v string, cause lostCause) {
	keys := append([]string(nil), (*fs.byVar)[v]...)
	for _, k := range keys {
		f, ok := (*fs.facts)[k]
		if !ok {
			continue
		}
		fs.removeFact(k)
		fs.addLost(f, cause)
	}
}

func (fs *FactSet) constOf(v string) (int, bool) {
	f, ok := (*fs.facts)["K/"+v]
	if ok && f.kind == fkConst {
		return f.cval, true
	}
	return 0, false
}

// equal compares fact content (not sources) for fixpoint detection.
func (fs *FactSet) equal(o *FactSet) bool {
	if fs.reachable != o.reachable {
		return false
	}
	if fs.facts == o.facts && fs.lost == o.lost {
		return true
	}
	if len(*fs.facts) != len(*o.facts) || len(*fs.lost) != len(*o.lost) {
		return false
	}
	for k, f := range *fs.facts {
		g, ok := (*o.facts)[k]
		if !ok || !factEqual(f, g) {
			return false
		}
	}
	for k, l := range *fs.lost {
		m, ok := (*o.lost)[k]
		if !ok || l.cause != m.cause || !factEqual(l.f, m.f) {
			return false
		}
	}
	return true
}

// meet intersects two fact sets at a control-flow join. lp is non-nil
// when the join is a loop header, so losses caused by loop-body
// assignments are attributed correctly. Only the smaller side is
// scanned; identical shared maps cost O(1).
func meet(a, b *FactSet, lp *Loop, st *Stats) *FactSet {
	out := newFactSet(a.reachable || b.reachable)
	if a.lost == b.lost {
		out.lost = a.lost
		out.lostByVar = a.lostByVar
	} else {
		for _, l := range *a.lost {
			st.MeetOps++
			out.addLost(l.f, l.cause)
		}
		for _, l := range *b.lost {
			st.MeetOps++
			out.addLost(l.f, l.cause)
		}
	}
	if a.facts == b.facts {
		out.facts = a.facts
		out.byVar = a.byVar
	} else {
		small, large := a, b
		if len(*b.facts) < len(*a.facts) {
			small, large = b, a
		}
		for k, f := range *small.facts {
			st.MeetOps++
			g, ok := (*large.facts)[k]
			if ok && factEqual(f, g) {
				if g.src < f.src {
					f.src = g.src
				}
				out.addFact(f)
			} else {
				out.addLost(f, joinCause(f, lp))
			}
		}
		// Facts present only on the larger side are lost here too;
		// record them for diagnostics. Cost stays proportional to the
		// facts on these two paths, never to unrelated variables.
		for k, f := range *large.facts {
			st.MeetOps++
			if _, ok := (*small.facts)[k]; !ok {
				out.addLost(f, joinCause(f, lp))
			}
		}
	}
	return out
}

func joinCause(f fact, lp *Loop) lostCause {
	if lp != nil {
		for _, v := range f.vars() {
			if lp.Assigned[v] || lp.Arrays[v] {
				return lcLoop
			}
		}
	}
	return lcJoin
}

// transferInstr applies one non-terminator instruction.
func transferInstr(fs *FactSet, in Instr, src string) *FactSet {
	if !fs.reachable {
		return fs
	}
	switch in.Kind {
	case OpAssignConst:
		fs.killVar(in.Dst, lcAssign)
		fs.addFact(fact{kind: fkConst, src: src, cval: in.C, arr: in.Dst})
	case OpAssign:
		fs.killVar(in.Dst, lcAssign)
		if v, ok := fs.constOf(in.Src); ok {
			fs.addFact(fact{kind: fkConst, src: src, cval: v, arr: in.Dst})
		}
	case OpAssignAdd:
		fs.killVar(in.Dst, lcAssign)
		if v, ok := fs.constOf(in.Src); ok {
			fs.addFact(fact{kind: fkConst, src: src, cval: v + in.C, arr: in.Dst})
		}
	case OpNewArray:
		fs.killVar(in.Arr, lcArray)
		fs.addFact(fact{kind: fkLen, src: src, cval: in.C, arr: in.Arr})
	case OpAssignArray:
		fs.killVar(in.Arr, lcArray)
	case OpCheck:
		fs.addFact(fact{kind: fkCheck, src: fmt.Sprintf("check#%d", in.ID), arr: in.Arr, idx: in.Idx})
	}
	return fs
}

var negateOp = map[string]string{"<": ">=", "<=": ">", ">": "<=", ">=": "<", "==": "!=", "!=": "=="}

func evalCmp(op string, l, r int) (bool, bool) {
	switch op {
	case "<":
		return l < r, true
	case "<=":
		return l <= r, true
	case ">":
		return l > r, true
	case ">=":
		return l >= r, true
	case "==":
		return l == r, true
	case "!=":
		return l != r, true
	}
	return false, false
}

// normalizeCmp rewrites l op r so a variable is on the left. Both
// constant returns (result, true).
func normalizeCmp(l Operand, op string, r Operand) (Cmp, bool, bool) {
	if l.IsConst && r.IsConst {
		v, ok := evalCmp(op, l.Const, r.Const)
		return Cmp{}, v, ok
	}
	if l.IsConst {
		swapped := map[string]string{"<": ">", "<=": ">=", ">": "<", ">=": "<=", "==": "==", "!=": "!="}
		return Cmp{Left: r.Var, Op: swapped[op], RConst: true, RVal: l.Const}, false, true
	}
	c := Cmp{Left: l.Var, Op: op}
	if r.IsConst {
		c.RConst = true
		c.RVal = r.Const
	} else {
		c.RVar = r.Var
	}
	return c, false, true
}

// applyCond returns the fact set on the edge where l op r holds.
func applyCond(fs *FactSet, l Operand, op string, r Operand, src string) *FactSet {
	if !fs.reachable {
		return fs
	}
	// Work on a private struct copy: addFact clones the shared maps, so
	// the caller's set (used for the sibling edge) stays intact.
	cp := *fs
	fs = &cp
	if !l.IsConst {
		if v, ok := fs.constOf(l.Var); ok {
			l = ConstOp(v)
		}
	}
	if !r.IsConst {
		if v, ok := fs.constOf(r.Var); ok {
			r = ConstOp(v)
		}
	}
	c, constRes, ok := normalizeCmp(l, op, r)
	if !ok {
		return fs
	}
	if l.IsConst && r.IsConst {
		if constRes {
			return fs
		}
		return newFactSet(false)
	}
	fs.addFact(fact{kind: fkCmp, src: src, cmp: c})
	if c.Op == "==" && c.RConst {
		fs.addFact(fact{kind: fkConst, src: src, cval: c.RVal, arr: c.Left})
	}
	return fs
}

// edgeSets computes the outgoing fact sets per successor.
func edgeSets(fs *FactSet, t Term, block string) map[string]*FactSet {
	out := map[string]*FactSet{}
	switch t.Kind {
	case TJump:
		out[t.Target] = fs
	case TBranch:
		out[t.Then] = applyCond(fs, t.Left, t.Op, t.Right, block+":cond-true")
		out[t.Else] = applyCond(fs, t.Left, negateOp[t.Op], t.Right, block+":cond-false")
	}
	return out
}

// sortedKeys is a helper for deterministic iteration.
func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
