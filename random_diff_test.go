package ontology

import (
	"math/rand"
	"strconv"
	"testing"
)

// genCtx 是随机程序生成上下文；论域固定为小而完备的具体值集合。
type genCtx struct {
	rng    *rand.Rand
	prog   *Program
	u      *naiveUniverse
	shapes []*Type
	idN    int
}

func newGenCtx(seed int64) *genCtx {
	shapeLeafKeys = map[string]string{}
	objectPropVals = map[string]map[string][]val{}

	g := &genCtx{
		rng:  rand.New(rand.NewSource(seed)),
		u:    &naiveUniverse{numbers: []float64{0, 1, 2}, strings: []string{"", "a", "b"}},
		prog: &Program{Decls: map[string]*Type{}},
	}
	mkShape := func(name string, tags []string, other *Type) *Type {
		leafTypes := make([]*Type, 0, len(tags))
		for _, tg := range tags {
			leafTypes = append(leafTypes, TStrLit(tg))
		}
		var kindType *Type
		if len(leafTypes) == 1 {
			kindType = leafTypes[0]
		} else {
			kindType = TUnion(leafTypes...)
		}
		obj := TObject(map[string]*Type{"kind": kindType, "v": other})
		n, _ := normalize(obj)
		shapeLeafKeys[name] = leafKey(n.members[0])
		g.u.objects = append(g.u.objects, name)
		vals := map[string][]val{}
		for _, tg := range tags {
			vals["kind"] = append(vals["kind"], val{tag: vStr, s: tg})
		}
		nn, _ := normalize(other)
		for v := range g.u.membersToVals(nn) {
			vals["v"] = append(vals["v"], v)
		}
		objectPropVals[name] = vals
		return obj
	}
	g.shapes = append(g.shapes,
		mkShape("Oa", []string{"a"}, TNumber()),
		mkShape("Ob", []string{"b"}, TString()),
		mkShape("Oac", []string{"a", "c"}, TBoolean()),
	)
	return g
}

func (g *genCtx) newID(prefix string) string {
	g.idN++
	return prefix + strconv.Itoa(g.idN)
}

func (g *genCtx) randLiteral() *Type {
	switch g.rng.Intn(5) {
	case 0:
		return TNumLit(g.u.numbers[g.rng.Intn(len(g.u.numbers))])
	case 1:
		return TStrLit(g.u.strings[g.rng.Intn(len(g.u.strings))])
	case 2:
		return TBoolLit(g.rng.Intn(2) == 0)
	case 3:
		return TNull()
	default:
		return TUndefined()
	}
}

// randUnion 生成受论域支持的标量/对象联合。
func (g *genCtx) randUnion() *Type {
	cand := []*Type{
		TNumber(), TString(), TBoolean(), TNull(), TUndefined(),
		TNumLit(g.u.numbers[g.rng.Intn(3)]),
		TNumLit(g.u.numbers[g.rng.Intn(3)]),
		TStrLit(g.u.strings[g.rng.Intn(3)]),
		TStrLit(g.u.strings[g.rng.Intn(3)]),
		g.shapes[0], g.shapes[1], g.shapes[2],
	}
	g.rng.Shuffle(len(cand), func(i, j int) { cand[i], cand[j] = cand[j], cand[i] })
	nb := 1 + g.rng.Intn(4)
	if nb > len(cand) {
		nb = len(cand)
	}
	if nb == 1 {
		return cand[0]
	}
	return TUnion(cand[:nb]...)
}

// assignValue 从声明成员中挑选一个必可赋值的类型。
func (g *genCtx) assignValue(decl *Type) *Type {
	n, _ := normalize(decl)
	ms := make([]*Type, 0, len(n.members))
	for _, lf := range n.members {
		ms = append(ms, leafToPublic(lf))
	}
	if len(ms) == 0 {
		return TNever()
	}
	pick := append([]*Type(nil), ms...)
	g.rng.Shuffle(len(pick), func(i, j int) { pick[i], pick[j] = pick[j], pick[i] })
	if len(pick) > 1 && g.rng.Intn(2) == 0 {
		k := 1 + g.rng.Intn(len(pick)-1)
		return TUnion(pick[:k]...)
	}
	return pick[0]
}

// randLeafCond 生成针对变量的叶子条件；对象联合允许生成判别属性条件。
func (g *genCtx) randLeafCond(variable string, decl *Type) *Cond {
	r := g.rng
	n, _ := normalize(decl)
	allObjKind := len(n.members) > 0
	for _, lf := range n.members {
		if lf.kind != lkObject {
			allObjKind = false
			break
		}
		if _, ok := lf.props["kind"]; !ok {
			allObjKind = false
			break
		}
	}
	if allObjKind && r.Intn(3) == 0 {
		return &Cond{K: CondDiscrim, Var: variable, Prop: "kind",
			Lit: TStrLit(g.u.strings[1+r.Intn(2)])}
	}
	switch r.Intn(8) {
	case 0:
		return &Cond{K: CondIsType, Var: variable, Check: CheckedType(r.Intn(5))}
	case 1:
		return &Cond{K: CondEqNull, Var: variable}
	case 2:
		return &Cond{K: CondEqUndefined, Var: variable}
	case 3:
		return &Cond{K: CondLooseNull, Var: variable}
	case 4:
		return &Cond{K: CondTruthy, Var: variable}
	default:
		return &Cond{K: CondEqLiteral, Var: variable, Lit: g.randLiteral()}
	}
}

func (g *genCtx) randCond(variable string, decl *Type, depth int) *Cond {
	if depth <= 0 {
		return g.randLeafCond(variable, decl)
	}
	switch g.rng.Intn(10) {
	case 0, 1:
		return &Cond{K: CondNot, Inner: g.randCond(variable, decl, depth-1)}
	case 2, 3:
		return &Cond{K: CondAnd,
			Left: g.randCond(variable, decl, depth-1), Right: g.randLeafCond(variable, decl)}
	case 4, 5:
		return &Cond{K: CondOr,
			Left: g.randCond(variable, decl, depth-1), Right: g.randLeafCond(variable, decl)}
	default:
		return g.randLeafCond(variable, decl)
	}
}

func (g *genCtx) genBlock(stmts *[]*Stmt, variables []string, depth int) {
	n := 1 + g.rng.Intn(3)
	for i := 0; i < n; i++ {
		variable := variables[g.rng.Intn(len(variables))]
		decl := g.prog.Decls[variable]
		switch g.rng.Intn(10) {
		case 0:
			*stmts = append(*stmts, &Stmt{ID: g.newID("ret"), K: StmtReturn})
			return
		case 1, 2:
			*stmts = append(*stmts, &Stmt{ID: g.newID("asg"), K: StmtAssign,
				Var: variable, Value: g.assignValue(decl)})
		default:
			ift := &Stmt{ID: g.newID("if"), K: StmtIf, Cond: g.randCond(variable, decl, 2)}
			if depth > 0 {
				if g.rng.Intn(2) == 0 {
					g.genBlock(&ift.Then, variables, depth-1)
				}
				if g.rng.Intn(2) == 0 {
					g.genBlock(&ift.Else, variables, depth-1)
				}
			}
			// 空分支放自赋值探针，确保真假两环境都有可查询的点且不改变汇合结果。
			if len(ift.Then) == 0 {
				ift.Then = []*Stmt{{ID: g.newID("thenP"), K: StmtAssign,
					Var: variable, Value: g.assignValue(decl)}}
			}
			if len(ift.Else) == 0 {
				ift.Else = []*Stmt{{ID: g.newID("elseP"), K: StmtAssign,
					Var: variable, Value: g.assignValue(decl)}}
			}
			*stmts = append(*stmts, ift)
		}
	}
}

func generateProgram(seed int64) *genCtx {
	g := newGenCtx(seed)
	variables := []string{"x", "y", "e"}
	g.prog.Decls["x"] = g.randUnion()
	g.prog.Decls["y"] = g.randUnion()
	g.prog.Decls["e"] = TUnion(g.shapes...)
	g.genBlock(&g.prog.Stmts, variables, 2)
	return g
}

func countStmts(stmts []*Stmt) int {
	c := 0
	var walk func([]*Stmt)
	walk = func(ss []*Stmt) {
		for _, s := range ss {
			c++
			if s.K == StmtIf {
				walk(s.Then)
				walk(s.Else)
			}
		}
	}
	walk(stmts)
	return c
}

func sameValSet(a, b valSet) bool {
	a = concretize(a)
	b = concretize(b)
	if len(a) != len(b) {
		return false
	}
	for v := range a {
		if !b[v] {
			return false
		}
	}
	return true
}

// concretize 把原子占位值展开为论域中全部对应具体值，并与已存在的具体值去重，
// 使“原子 number/string”与其具体成员在比较时语义一致。
func concretize(s valSet) valSet {
	out := valSet{}
	for v := range s {
		if !v.atom {
			out[v] = true
			continue
		}
		// 原子占位：其具体形态就是所有同类型值（atom 标志去掉）。
		// atom 占位本身也代表整个论域，故无需再放带标志的键。
		switch v.tag {
		case vNum:
			for _, n := range concreteNumbers {
				out[val{tag: vNum, n: n}] = true
			}
		case vStr:
			for _, str := range concreteStrings {
				out[val{tag: vStr, s: str}] = true
			}
		default:
			v.atom = false
			out[v] = true
		}
	}
	return out
}

var concreteNumbers = []float64{0, 1, 2}
var concreteStrings = []string{"", "a", "b"}

func renderWorldVar(e worldEnv, variable string) string {
	if e == nil {
		return "<unreachable>"
	}
	return renderValSet(e[variable])
}

// TestRandomDifferential 用大量随机程序对照朴素枚举模型，逐点逐变量验证取值集合。
func TestRandomDifferential(t *testing.T) {
	const iterations = 300
	l := newLogger(t, "random-differential")
	defer l.finish()
	totalStmts := 0
	for seed := int64(1); seed <= iterations; seed++ {
		g := generateProgram(seed)
		nStmt := countStmts(g.prog.Stmts)
		totalStmts += nStmt
		l.input("seed=%d 语句数=%d", seed, nStmt)

		res, aerr := Analyze(t.Context(), g.prog)
		if aerr != nil {
			l.output("seed=%d 分析失败（错误程序，朴素模型跳过）: %v", seed, aerr)
			continue
		}
		nr := runNaive(g.prog, g.u)

		disagreements := 0
		var walk func([]*Stmt)
		walk = func(stmts []*Stmt) {
			for _, s := range stmts {
				for _, variable := range []string{"x", "y", "e"} {
					pr, qerr := res.Query(s.ID, variable)
					nw, nok := nr.entry[s.ID]
					if qerr != nil {
						l.fatalf("seed=%d 点 %s 查询出错: %v", seed, s.ID, qerr)
					}
					if !nok {
						l.fatalf("seed=%d 朴素模型缺少点 %s", seed, s.ID)
					}
					if pr.Status == PointReachable && nw == nil {
						disagreements++
						l.reason("seed=%d 点 %s/%s: 符号=可达(%s) 朴素=不可达",
							seed, s.ID, variable, renderType(pr.Type))
						continue
					}
					if pr.Status == PointUnreachable && nw != nil {
						disagreements++
						l.reason("seed=%d 点 %s/%s: 符号=不可达 朴素=%s",
							seed, s.ID, variable, renderWorldVar(nw, variable))
						continue
					}
					if nw == nil {
						continue
					}
					symSet := g.u.membersToVals(mustNormalize(pr.Type))
					if !sameValSet(symSet, nw[variable]) {
						disagreements++
						l.reason("seed=%d 点 %s/%s 分歧: 符号=%s 朴素=%s",
							seed, s.ID, variable, renderValSet(symSet), renderValSet(nw[variable]))
					}
				}
				if s.K == StmtIf {
					walk(s.Then)
					walk(s.Else)
				}
			}
		}
		walk(g.prog.Stmts)
		l.output("seed=%d 对照完成，分歧=%d", seed, disagreements)
		if disagreements != 0 {
			l.fatalf("seed=%d 发现 %d 处分歧", seed, disagreements)
		}
	}
	l.reason("共 %d 个随机程序、%d 条语句，全部与朴素枚举模型一致", iterations, totalStmts)
}
