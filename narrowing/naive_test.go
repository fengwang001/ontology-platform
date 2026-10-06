package narrowing

import (
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This file contains an independent naive model of the semantics: it
// enumerates every concrete value of every declared type and executes
// the program over sets of concrete states. Randomly generated programs
// are checked against the analyzer: both must agree on the reachability
// of every program point, and every concretely reachable value must be
// covered by the analyzer's narrowed type (soundness). Each checked
// program, its outputs and the verdicts are logged.

// ---------- concrete values ----------

type vkind int

const (
	vNum vkind = iota
	vStr
	vBool
	vNull
	vUndef
	vObj
)

type cval struct {
	kind vkind
	num  float64
	str  string
	flag bool
	obj  map[string]cval
}

// Pools shared by the generator and the enumerator so that every
// literal a random program can test is also enumerable.
var numPool = []float64{0, 1, 2.5}
var strPool = []string{"", "a", "b"}

func (v cval) key() string {
	switch v.kind {
	case vNum:
		return "n:" + strconv.FormatFloat(v.num, 'g', -1, 64)
	case vStr:
		return "s:" + strconv.Quote(v.str)
	case vBool:
		return "b:" + strconv.FormatBool(v.flag)
	case vNull:
		return "null"
	case vUndef:
		return "undef"
	case vObj:
		names := make([]string, 0, len(v.obj))
		for name := range v.obj {
			names = append(names, name)
		}
		sort.Strings(names)
		var b strings.Builder
		b.WriteString("{")
		for _, name := range names {
			b.WriteString(strconv.Quote(name))
			b.WriteString(":")
			b.WriteString(v.obj[name].key())
			b.WriteString(";")
		}
		b.WriteString("}")
		return b.String()
	}
	return "?"
}

func (v cval) String() string { return v.key() }

// valuesOf enumerates concrete values of a normalized type.
func valuesOf(t Type) []cval {
	var out []cval
	for _, m := range t.Members() {
		switch m.kind {
		case KindNumber:
			for _, n := range numPool {
				out = append(out, cval{kind: vNum, num: n})
			}
		case KindString:
			for _, s := range strPool {
				out = append(out, cval{kind: vStr, str: s})
			}
		case KindNull:
			out = append(out, cval{kind: vNull})
		case KindUndefined:
			out = append(out, cval{kind: vUndef})
		case KindNumberLiteral:
			out = append(out, cval{kind: vNum, num: m.num})
		case KindStringLiteral:
			out = append(out, cval{kind: vStr, str: m.str})
		case KindBooleanLiteral:
			out = append(out, cval{kind: vBool, flag: m.flag})
		case KindObject:
			names := make([]string, 0, len(m.props))
			for name := range m.props {
				names = append(names, name)
			}
			sort.Strings(names)
			combos := []map[string]cval{{}}
			for _, name := range names {
				propVals := valuesOf(m.props[name])
				var next []map[string]cval
				for _, combo := range combos {
					for _, pv := range propVals {
						copied := make(map[string]cval, len(combo)+1)
						for k, v := range combo {
							copied[k] = v
						}
						copied[name] = pv
						next = append(next, copied)
					}
				}
				combos = next
				if len(combos) > 64 {
					combos = combos[:64] // keep enumeration bounded
				}
			}
			for _, combo := range combos {
				out = append(out, cval{kind: vObj, obj: combo})
			}
		}
	}
	if len(out) > 128 {
		out = out[:128]
	}
	return out
}

// valueInMember reports whether a concrete value belongs to a member.
func valueInMember(v cval, m Member) bool {
	switch v.kind {
	case vNum:
		return m.kind == KindNumber || (m.kind == KindNumberLiteral && m.num == v.num)
	case vStr:
		return m.kind == KindString || (m.kind == KindStringLiteral && m.str == v.str)
	case vBool:
		return m.kind == KindBooleanLiteral && m.flag == v.flag
	case vNull:
		return m.kind == KindNull
	case vUndef:
		return m.kind == KindUndefined
	case vObj:
		if m.kind != KindObject || len(m.props) != len(v.obj) {
			return false
		}
		for name, pv := range v.obj {
			pt, ok := m.props[name]
			if !ok || !valueInType(pv, pt) {
				return false
			}
		}
		return true
	}
	return false
}

func valueInType(v cval, t Type) bool {
	for _, m := range t.Members() {
		if valueInMember(v, m) {
			return true
		}
	}
	return false
}

func truthyVal(v cval) bool {
	switch v.kind {
	case vNull, vUndef:
		return false
	case vBool:
		return v.flag
	case vNum:
		return v.num != 0
	case vStr:
		return v.str != ""
	case vObj:
		return true
	}
	return false
}

func typeofVal(v cval, kind TypeOfKind) bool {
	switch kind {
	case TypeOfNumber:
		return v.kind == vNum
	case TypeOfString:
		return v.kind == vStr
	case TypeOfBoolean:
		return v.kind == vBool
	case TypeOfObject:
		return v.kind == vObj || v.kind == vNull
	case TypeOfUndefined:
		return v.kind == vUndef
	}
	return false
}

func litEqualVal(v cval, lit Literal) bool {
	switch lit.Kind {
	case LitNumber:
		return v.kind == vNum && v.num == lit.Num
	case LitString:
		return v.kind == vStr && v.str == lit.Str
	case LitBoolean:
		return v.kind == vBool && v.flag == lit.Bool
	}
	return false
}

// ---------- concrete interpreter ----------

type cstate map[string]cval

func stateKey(s cstate) string {
	names := make([]string, 0, len(s))
	for name := range s {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		b.WriteString(name)
		b.WriteString("=")
		b.WriteString(s[name].key())
		b.WriteString(";")
	}
	return b.String()
}

type stateSet map[string]cstate

func (ss stateSet) add(s cstate) { ss[stateKey(s)] = s }

func (ss stateSet) union(other stateSet) stateSet {
	out := stateSet{}
	for k, v := range ss {
		out[k] = v
	}
	for k, v := range other {
		out[k] = v
	}
	return out
}

// pointValues records, per statement and point, the set of concrete
// values each variable can hold.
type pointValues map[StatementID]map[Point]map[string]map[string]cval

func (pv pointValues) record(id StatementID, p Point, states stateSet) {
	byPoint, ok := pv[id]
	if !ok {
		byPoint = map[Point]map[string]map[string]cval{}
		pv[id] = byPoint
	}
	byVar, ok := byPoint[p]
	if !ok {
		byVar = map[string]map[string]cval{}
		byPoint[p] = byVar
	}
	for _, st := range states {
		for name, v := range st {
			set, ok := byVar[name]
			if !ok {
				set = map[string]cval{}
				byVar[name] = set
			}
			set[v.key()] = v
		}
	}
}

func execBlockC(stmts []Stmt, states stateSet, pv pointValues) stateSet {
	for _, s := range stmts {
		pv.record(s.StmtID(), Before, states)
		states = execStmtC(s, states, pv)
		pv.record(s.StmtID(), After, states)
	}
	return states
}

func execStmtC(s Stmt, states stateSet, pv pointValues) stateSet {
	if len(states) == 0 {
		return states
	}
	switch s := s.(type) {
	case *Assign:
		typ, err := s.Type.normalize()
		if err != nil {
			panic(err)
		}
		vals := valuesOf(typ)
		out := stateSet{}
		for _, st := range states {
			for _, v := range vals {
				next := make(cstate, len(st))
				for name, val := range st {
					next[name] = val
				}
				next[s.Var] = v
				out.add(next)
				if len(out) > 200000 {
					panic("state explosion")
				}
			}
		}
		return out
	case *Return:
		return stateSet{}
	case *If:
		tStates, fStates := splitCondC(s.Cond, states)
		return execBlockC(s.Then, tStates, pv).union(execBlockC(s.Else, fStates, pv))
	}
	return states
}

func splitCondC(c Cond, states stateSet) (stateSet, stateSet) {
	switch c := c.(type) {
	case Not:
		t, f := splitCondC(c.C, states)
		return f, t
	case And:
		lt, lf := splitCondC(c.L, states)
		rt, rf := splitCondC(c.R, lt)
		return rt, lf.union(rf)
	case Or:
		lt, lf := splitCondC(c.L, states)
		rt, rf := splitCondC(c.R, lf)
		return lt.union(rt), rf
	}
	match := func(v cval) bool { return false }
	var varName string
	switch c := c.(type) {
	case TypeOf:
		varName = c.Var
		match = func(v cval) bool { return typeofVal(v, c.Kind) }
	case EqNull:
		varName = c.Var
		match = func(v cval) bool { return v.kind == vNull }
	case EqUndefined:
		varName = c.Var
		match = func(v cval) bool { return v.kind == vUndef }
	case EqLiteral:
		varName = c.Var
		match = func(v cval) bool { return litEqualVal(v, c.Lit) }
	case LooseEqNull:
		varName = c.Var
		match = func(v cval) bool { return v.kind == vNull || v.kind == vUndef }
	case Truthy:
		varName = c.Var
		match = truthyVal
	case PropEq:
		varName = c.Var
		match = func(v cval) bool { return litEqualVal(v.obj[c.Prop], c.Lit) }
	}
	t, f := stateSet{}, stateSet{}
	for key, st := range states {
		if match(st[varName]) {
			t[key] = st
		} else {
			f[key] = st
		}
	}
	return t, f
}

// ---------- program pretty printing (for logs) ----------

func printProgram(decls []Decl, body []Stmt) string {
	var b strings.Builder
	for _, d := range decls {
		typ, err := d.Type.normalize()
		if err != nil {
			fmt.Fprintf(&b, "var %s: <malformed>\n", d.Name)
			continue
		}
		fmt.Fprintf(&b, "var %s: %s\n", d.Name, typ)
	}
	printStmts(&b, body, 0)
	return b.String()
}

func printStmts(b *strings.Builder, stmts []Stmt, depth int) {
	pad := strings.Repeat("  ", depth)
	for _, s := range stmts {
		switch s := s.(type) {
		case *Assign:
			typ, err := s.Type.normalize()
			ts := "<malformed>"
			if err == nil {
				ts = typ.String()
			}
			fmt.Fprintf(b, "%s[%s] %s = (%s)\n", pad, s.ID, s.Var, ts)
		case *Return:
			fmt.Fprintf(b, "%s[%s] return\n", pad, s.ID)
		case *If:
			fmt.Fprintf(b, "%s[%s] if %s\n", pad, s.ID, printCond(s.Cond))
			printStmts(b, s.Then, depth+1)
			if len(s.Else) > 0 {
				fmt.Fprintf(b, "%selse\n", pad)
				printStmts(b, s.Else, depth+1)
			}
		}
	}
}

func printCond(c Cond) string {
	switch c := c.(type) {
	case TypeOf:
		return fmt.Sprintf("typeof %s == %d", c.Var, c.Kind)
	case EqNull:
		return c.Var + " === null"
	case EqUndefined:
		return c.Var + " === undefined"
	case EqLiteral:
		return fmt.Sprintf("%s === %s", c.Var, printLit(c.Lit))
	case LooseEqNull:
		return c.Var + " == null"
	case Truthy:
		return c.Var
	case PropEq:
		return fmt.Sprintf("%s.%s === %s", c.Var, c.Prop, printLit(c.Lit))
	case Not:
		return "!(" + printCond(c.C) + ")"
	case And:
		return "(" + printCond(c.L) + " && " + printCond(c.R) + ")"
	case Or:
		return "(" + printCond(c.L) + " || " + printCond(c.R) + ")"
	}
	return "?"
}

func printLit(l Literal) string {
	switch l.Kind {
	case LitNumber:
		return strconv.FormatFloat(l.Num, 'g', -1, 64)
	case LitString:
		return strconv.Quote(l.Str)
	case LitBoolean:
		return strconv.FormatBool(l.Bool)
	}
	return "?"
}

func collectStmtIDs(stmts []Stmt, out *[]StatementID) {
	for _, s := range stmts {
		*out = append(*out, s.StmtID())
		if ifs, ok := s.(*If); ok {
			collectStmtIDs(ifs.Then, out)
			collectStmtIDs(ifs.Else, out)
		}
	}
}

// ---------- differential check ----------

// checkAgainstNaive runs the analyzer and the naive concrete model on
// the same program and compares every program point.
func checkAgainstNaive(t *testing.T, decls []Decl, body []Stmt) {
	t.Helper()
	res, err := Analyze(decls, body)
	if err != nil {
		t.Fatalf("analysis of generated program failed: %v\n%s", err, printProgram(decls, body))
	}

	// Naive model: cartesian product of initial values.
	initial := stateSet{}
	states := []cstate{{}}
	for _, d := range decls {
		typ, _ := d.Type.normalize()
		vals := valuesOf(typ)
		var next []cstate
		for _, st := range states {
			for _, v := range vals {
				copied := make(cstate, len(st)+1)
				for name, val := range st {
					copied[name] = val
				}
				copied[d.Name] = v
				next = append(next, copied)
			}
		}
		states = next
		if len(states) > 20000 {
			states = states[:20000]
		}
	}
	for _, st := range states {
		initial.add(st)
	}
	pv := pointValues{}
	execBlockC(body, initial, pv)

	var ids []StatementID
	collectStmtIDs(body, &ids)

	declaredTypes := map[string]Type{}
	for _, d := range decls {
		typ, _ := d.Type.normalize()
		declaredTypes[d.Name] = typ
	}

	for _, id := range ids {
		for _, p := range []Point{Before, After} {
			for _, d := range decls {
				typ, reachable, ok := res.Query(id, p, d.Name)
				if !ok {
					t.Fatalf("query failed for %s", id)
				}
				var naiveVals map[string]cval
				if byPoint, found := pv[id]; found {
					if byVar, found := byPoint[p]; found {
						naiveVals = byVar[d.Name]
					}
				}
				naiveReachable := len(naiveVals) > 0
				if naiveReachable && !reachable {
					t.Fatalf("unsound reachability at %s(%v) var %s: naive model reaches a point the analyzer calls unreachable\nprogram:\n%s",
						id, p, d.Name, printProgram(decls, body))
				}
				if reachable && !naiveReachable {
					// The analyzer is a non-relational over-approximation:
					// joins keep per-variable unions and lose correlations
					// between variables, so it may consider a point
					// reachable that no concrete execution reaches. This
					// direction is precision, not soundness.
					t.Logf("  point %s/%v var %s: analyzer=reachable naive=unreachable verdict=OK (over-approximation)",
						id, map[Point]string{Before: "before", After: "after"}[p], d.Name)
					continue
				}
				if !reachable {
					continue
				}
				for _, v := range naiveVals {
					if !valueInType(v, typ) {
						t.Fatalf("unsound at %s(%v): value %s of %s not in narrowed type %s\nprogram:\n%s",
							id, p, v, d.Name, typ, printProgram(decls, body))
					}
				}
				if !declaredTypes[d.Name].ContainsType(typ) {
					t.Fatalf("narrowed type %s of %s escapes declared type %s at %s(%v)",
						typ, d.Name, declaredTypes[d.Name], id, p)
				}
				t.Logf("  point %s/%v var %s: narrowed=%s naive-values=%d verdict=OK (reachable, sound)",
					id, map[Point]string{Before: "before", After: "after"}[p], d.Name, typ, len(naiveVals))
			}
		}
	}
	t.Logf("verdict: %d statements x 2 points x %d vars all consistent with naive model",
		len(ids), len(decls))
}

// ---------- random program generator ----------

type progGen struct {
	r        *rand.Rand
	nextID   int
	decls    []Decl
	declared map[string]Type
	vars     []string
}

func (g *progGen) stmtID() StatementID {
	g.nextID++
	return StatementID(fmt.Sprintf("s%d", g.nextID))
}

func memberExpr(m Member) TypeExpr {
	switch m.kind {
	case KindNumber:
		return NumberExpr()
	case KindString:
		return StringExpr()
	case KindNull:
		return NullExpr()
	case KindUndefined:
		return UndefinedExpr()
	case KindNumberLiteral:
		return NumLitExpr(m.num)
	case KindStringLiteral:
		return StrLitExpr(m.str)
	case KindBooleanLiteral:
		return BoolLitExpr(m.flag)
	case KindObject:
		var props []PropExpr
		for name, pt := range m.props {
			props = append(props, Prop(name, typeExprOf(pt)))
		}
		sort.Slice(props, func(i, j int) bool { return props[i].Name < props[j].Name })
		return ObjectExpr(props...)
	}
	panic("unknown member")
}

func typeExprOf(t Type) TypeExpr {
	var exprs []TypeExpr
	for _, m := range t.Members() {
		exprs = append(exprs, memberExpr(m))
	}
	return UnionExpr(exprs...)
}

// genLeafType builds one random union alternative.
func (g *progGen) genLeafType(depth int) TypeExpr {
	choice := g.r.Intn(10)
	switch choice {
	case 0:
		return NumberExpr()
	case 1:
		return StringExpr()
	case 2:
		return BooleanExpr()
	case 3:
		return NullExpr()
	case 4:
		return UndefinedExpr()
	case 5:
		return NumLitExpr(numPool[g.r.Intn(len(numPool))])
	case 6:
		return StrLitExpr(strPool[g.r.Intn(len(strPool))])
	case 7:
		return BoolLitExpr(g.r.Intn(2) == 0)
	default: // object with 1-2 simple properties
		if depth > 1 {
			return NumberExpr()
		}
		props := []PropExpr{Prop("k", g.genLeafType(depth+1))}
		if g.r.Intn(2) == 0 {
			props = append(props, Prop("v", g.genLeafType(depth+1)))
		}
		return ObjectExpr(props...)
	}
}

func (g *progGen) genType() TypeExpr {
	n := 1 + g.r.Intn(3)
	var exprs []TypeExpr
	for i := 0; i < n; i++ {
		exprs = append(exprs, g.genLeafType(0))
	}
	return UnionExpr(exprs...)
}

// genSubtypeExpr builds a random assignable subtype of the declared
// type: a subset of its members, with atomic members sometimes replaced
// by literals. Occasionally the empty (never) type, which makes the
// rest of the path unreachable.
func (g *progGen) genSubtypeExpr(declared Type) TypeExpr {
	if g.r.Intn(20) == 0 {
		return NeverExpr()
	}
	var exprs []TypeExpr
	for _, m := range declared.Members() {
		if g.r.Intn(2) != 0 {
			continue
		}
		switch m.kind {
		case KindNumber:
			if g.r.Intn(2) == 0 {
				exprs = append(exprs, NumLitExpr(numPool[g.r.Intn(len(numPool))]))
			} else {
				exprs = append(exprs, NumberExpr())
			}
		case KindString:
			if g.r.Intn(2) == 0 {
				exprs = append(exprs, StrLitExpr(strPool[g.r.Intn(len(strPool))]))
			} else {
				exprs = append(exprs, StringExpr())
			}
		default:
			exprs = append(exprs, memberExpr(m))
		}
	}
	if len(exprs) == 0 {
		members := declared.Members()
		exprs = append(exprs, memberExpr(members[g.r.Intn(len(members))]))
	}
	return UnionExpr(exprs...)
}

func (g *progGen) randomVar() string { return g.vars[g.r.Intn(len(g.vars))] }

func (g *progGen) randomLiteral() Literal {
	switch g.r.Intn(3) {
	case 0:
		return NumLit(numPool[g.r.Intn(len(numPool))])
	case 1:
		return StrLit(strPool[g.r.Intn(len(strPool))])
	default:
		return BoolLit(g.r.Intn(2) == 0)
	}
}

// discriminantProps returns, for variables whose declared type consists
// only of object members, the property names shared by all members.
func (g *progGen) discriminantProps(varName string) []string {
	declared := g.declared[varName]
	var common map[string]bool
	for _, m := range declared.Members() {
		if m.kind != KindObject {
			return nil
		}
		if common == nil {
			common = map[string]bool{}
			for name := range m.props {
				common[name] = true
			}
			continue
		}
		for name := range common {
			if _, ok := m.props[name]; !ok {
				delete(common, name)
			}
		}
	}
	var out []string
	for name := range common {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (g *progGen) genLeafCond() Cond {
	varName := g.randomVar()
	// Try a discriminant-property condition when possible.
	if props := g.discriminantProps(varName); len(props) > 0 && g.r.Intn(3) == 0 {
		prop := props[g.r.Intn(len(props))]
		// Pick a literal class compatible with some member's property
		// type so the condition is meaningful.
		for _, m := range g.declared[varName].Members() {
			pt := m.props[prop]
			switch {
			case pt.ContainsType(NumberLiteral(0)):
				return PropEq{Var: varName, Prop: prop, Lit: NumLit(numPool[g.r.Intn(len(numPool))])}
			case pt.ContainsType(StringLiteral("")):
				return PropEq{Var: varName, Prop: prop, Lit: StrLit(strPool[g.r.Intn(len(strPool))])}
			case pt.ContainsType(BooleanLiteral(true)) || pt.ContainsType(BooleanLiteral(false)):
				return PropEq{Var: varName, Prop: prop, Lit: BoolLit(g.r.Intn(2) == 0)}
			}
		}
	}
	switch g.r.Intn(7) {
	case 0:
		return TypeOf{Var: varName, Kind: TypeOfKind(g.r.Intn(5))}
	case 1:
		return EqNull{Var: varName}
	case 2:
		return EqUndefined{Var: varName}
	case 3:
		return EqLiteral{Var: varName, Lit: g.randomLiteral()}
	case 4:
		return LooseEqNull{Var: varName}
	case 5:
		return Truthy{Var: varName}
	default:
		return Not{C: Truthy{Var: varName}}
	}
}

func (g *progGen) genCond(depth int) Cond {
	if depth >= 2 {
		return g.genLeafCond()
	}
	switch g.r.Intn(4) {
	case 0:
		return And{L: g.genCond(depth + 1), R: g.genCond(depth + 1)}
	case 1:
		return Or{L: g.genCond(depth + 1), R: g.genCond(depth + 1)}
	case 2:
		return Not{C: g.genCond(depth + 1)}
	default:
		return g.genLeafCond()
	}
}

func (g *progGen) genBlock(depth, maxStmts int) []Stmt {
	n := 1 + g.r.Intn(maxStmts)
	var stmts []Stmt
	for i := 0; i < n; i++ {
		stmts = append(stmts, g.genStmt(depth))
	}
	return stmts
}

func (g *progGen) genStmt(depth int) Stmt {
	roll := g.r.Intn(10)
	if depth < 2 && roll < 4 {
		return &If{
			ID:   g.stmtID(),
			Cond: g.genCond(0),
			Then: g.genBlock(depth+1, 3),
			Else: g.genBlock(depth+1, 2),
		}
	}
	if roll < 8 {
		varName := g.randomVar()
		return &Assign{ID: g.stmtID(), Var: varName, Type: g.genSubtypeExpr(g.declared[varName])}
	}
	return &Return{ID: g.stmtID()}
}

func newProgram(seed int64) ([]Decl, []Stmt) {
	g := &progGen{
		r:        rand.New(rand.NewSource(seed)),
		declared: map[string]Type{},
	}
	names := []string{"a", "b", "c"}
	varCount := 1 + g.r.Intn(3)
	for i := 0; i < varCount; i++ {
		expr := g.genType()
		typ, err := expr.normalize()
		if err != nil || typ.IsNever() {
			expr = NumberExpr()
			typ = Number()
		}
		g.decls = append(g.decls, Decl{Name: names[i], Type: expr})
		g.declared[names[i]] = typ
		g.vars = append(g.vars, names[i])
	}
	return g.decls, g.genBlock(0, 8)
}

// TestRandomProgramsAgainstNaiveModel generates many pseudo-random
// programs and checks the analyzer against the independent naive model
// that enumerates all concrete values. Every program, its per-point
// outputs and the verdicts are written to the test log.
func TestRandomProgramsAgainstNaiveModel(t *testing.T) {
	const programs = 300
	for seed := int64(1); seed <= programs; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			decls, body := newProgram(seed)
			t.Logf("input program (seed=%d):\n%s", seed, printProgram(decls, body))
			checkAgainstNaive(t, decls, body)
		})
	}
}
