package bce

import "sort"

// Reason vocabulary for kept checks, in report order.
const (
	ReasonLower           = "下界未证"
	ReasonUpper           = "上界未证"
	ReasonArrayReassigned = "数组被重赋"
	ReasonJoinLoss        = "路径汇合丢失事实"
	ReasonLoopAssign      = "循环赋值使事实失效"
	ReasonOutOfScope      = "推理所需事实不在允许范围内"
)

// bounds collects the provable integer lower/upper bounds of a variable
// from constant and comparison facts, following at most one variable hop
// (x < v and v <= 5 gives x <= 4). Only allowed fact classes are used.
type bounds struct {
	lo, hi     int
	loOK, hiOK bool
	consulted  []fact
}

func (b *bounds) addLo(v int) {
	if !b.loOK || v > b.lo {
		b.lo, b.loOK = v, true
	}
}

func (b *bounds) addHi(v int) {
	if !b.hiOK || v < b.hi {
		b.hi, b.hiOK = v, true
	}
}

// boundsOf proves bounds for v. visited prevents recursion cycles.
func boundsOf(fs *FactSet, v string, visited map[string]bool, st *Stats) bounds {
	var b bounds
	if c, ok := fs.constOf(v); ok {
		st.FactsScanned++
		f := (*fs.facts)["K/"+v]
		b.consulted = append(b.consulted, f)
		b.addLo(c)
		b.addHi(c)
	}
	if visited[v] {
		return b
	}
	visited[v] = true
	for _, k := range (*fs.byVar)[v] {
		f := (*fs.facts)[k]
		if f.kind != fkCmp {
			continue
		}
		st.FactsScanned++
		b.consulted = append(b.consulted, f)
		c := f.cmp
		// Normalize to bounds on v.
		var rel string // relation of v to the other side
		var other Operand
		if c.Left == v {
			rel = c.Op
			if c.RConst {
				other = ConstOp(c.RVal)
			} else {
				other = VarOp(c.RVar)
			}
		} else if !c.RConst && c.RVar == v {
			rel = map[string]string{"<": ">", "<=": ">=", ">": "<", ">=": "<=", "==": "==", "!=": "!="}[c.Op]
			other = VarOp(c.Left)
		} else {
			continue
		}
		var lo, hi int
		var loOK, hiOK bool
		if other.IsConst {
			lo, hi, loOK, hiOK = other.Const, other.Const, true, true
		} else {
			ob := boundsOf(fs, other.Var, visited, st)
			b.consulted = append(b.consulted, ob.consulted...)
			lo, hi, loOK, hiOK = ob.lo, ob.hi, ob.loOK, ob.hiOK
		}
		switch rel {
		case "<":
			if hiOK {
				b.addHi(hi - 1)
			}
		case "<=":
			if hiOK {
				b.addHi(hi)
			}
		case ">":
			if loOK {
				b.addLo(lo + 1)
			}
		case ">=":
			if loOK {
				b.addLo(lo)
			}
		case "==":
			if loOK {
				b.addLo(lo)
			}
			if hiOK {
				b.addHi(hi)
			}
		}
	}
	return b
}

// lenLowerBound proves a lower bound LB with len(arr) >= LB, from
// constant-length facts and previously passed checks on arr.
func lenLowerBound(fs *FactSet, arr string, st *Stats) (int, bool, []fact) {
	lb := 0
	ok := false
	var consulted []fact
	if f, found := (*fs.facts)["L/"+arr]; found {
		st.FactsScanned++
		consulted = append(consulted, f)
		lb, ok = f.cval, true
	}
	for _, k := range (*fs.byVar)[arr] {
		f := (*fs.facts)[k]
		if f.kind != fkCheck {
			continue
		}
		st.FactsScanned++
		consulted = append(consulted, f)
		var wLo int
		var wOK bool
		if f.idx.IsConst {
			wLo, wOK = f.idx.Const, true
		} else {
			wb := boundsOf(fs, f.idx.Var, map[string]bool{}, st)
			consulted = append(consulted, wb.consulted...)
			wLo, wOK = wb.lo, wb.loOK
		}
		if wOK && (!ok || wLo+1 > lb) {
			lb, ok = wLo+1, true
		}
	}
	return lb, ok, consulted
}

// proof is the outcome of deciding one check.
type proof struct {
	lower, upper    bool
	consulted       []fact
	consultedBySide map[string][]fact // "lower"/"upper"
}

// proveCheck decides whether 0 <= idx < len(arr) is provable at fs.
// Cost depends only on facts mentioning idx and arr, never on program
// size; st.FactsScanned makes that verifiable.
func proveCheck(fs *FactSet, arr string, idx Operand, st *Stats) proof {
	var p proof
	p.consultedBySide = map[string][]fact{}
	// A previously passed identical check proves both sides at once.
	if f, ok := (*fs.facts)["P/"+arr+"/"+idx.String()]; ok {
		st.FactsScanned++
		p.lower, p.upper = true, true
		p.consulted = append(p.consulted, f)
		p.consultedBySide["lower"] = append(p.consultedBySide["lower"], f)
		p.consultedBySide["upper"] = append(p.consultedBySide["upper"], f)
		return p
	}
	// Index bounds.
	var ib bounds
	if idx.IsConst {
		st.FactsScanned++
		ib.addLo(idx.Const)
		ib.addHi(idx.Const)
	} else {
		ib = boundsOf(fs, idx.Var, map[string]bool{}, st)
	}
	p.consulted = append(p.consulted, ib.consulted...)
	p.consultedBySide["lower"] = append(p.consultedBySide["lower"], ib.consulted...)
	// Lower side: idx >= 0.
	p.lower = ib.loOK && ib.lo >= 0
	// Upper side: idx < len(arr), i.e. hi(idx) <= LB-1.
	lb, lenOK, lenConsulted := lenLowerBound(fs, arr, st)
	p.consulted = append(p.consulted, lenConsulted...)
	p.consultedBySide["upper"] = append(p.consultedBySide["upper"], ib.consulted...)
	p.consultedBySide["upper"] = append(p.consultedBySide["upper"], lenConsulted...)
	p.upper = lenOK && ib.hiOK && ib.hi <= lb-1
	return p
}

// relevantLost returns lost facts mentioning the check's var or array.
func relevantLost(fs *FactSet, arr string, idx Operand) []lost {
	seen := map[string]bool{}
	var out []lost
	names := []string{arr}
	if !idx.IsConst {
		names = append(names, idx.Var)
	}
	for _, n := range names {
		for _, k := range (*fs.lostByVar)[n] {
			if !seen[k] {
				seen[k] = true
				out = append(out, (*fs.lost)[k])
			}
		}
	}
	return out
}

// decideCheck produces the final decision for one check.
func decideCheck(fs *FactSet, arr string, idx Operand, st *Stats) (removed bool, reasons []string, consulted []fact) {
	if !fs.reachable {
		return true, nil, nil
	}
	p := proveCheck(fs, arr, idx, st)
	if p.lower && p.upper {
		return true, nil, p.consulted
	}
	if !p.lower {
		reasons = append(reasons, ReasonLower)
	}
	if !p.upper {
		reasons = append(reasons, ReasonUpper)
	}
	losts := relevantLost(fs, arr, idx)
	has := map[lostCause]bool{}
	for _, l := range losts {
		has[l.cause] = true
	}
	if has[lcArray] {
		reasons = append(reasons, ReasonArrayReassigned)
	}
	if has[lcJoin] {
		reasons = append(reasons, ReasonJoinLoss)
	}
	if has[lcLoop] {
		reasons = append(reasons, ReasonLoopAssign)
	}
	// Out of scope: an unproven side has neither usable facts nor any
	// lost fact that could have helped.
	scope := func(side string) bool {
		return len(p.consultedBySide[side]) == 0 && len(losts) == 0
	}
	if (!p.lower && scope("lower")) || (!p.upper && scope("upper")) {
		reasons = append(reasons, ReasonOutOfScope)
	}
	return false, reasons, p.consulted
}

// sortedFacts renders consulted facts deterministically.
func sortedFacts(fs []fact) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range fs {
		s := f.String() + " @ " + f.src
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
