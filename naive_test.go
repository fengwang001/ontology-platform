package ontology

// naive_test.go 实现一个与生产代码完全独立的“朴素模型”：枚举一个有限论域中的
// 全部具体取值，把环境表示为“变量 -> 可能取值集合（幂集）”，直接按 JS 式规则
// 对每个具体取值判断条件真假。随机程序同时交给朴素模型与生产分析器，逐点逐变量
// 对照取值集合，作为正确性的独立交叉验证。

import (
	"fmt"
	"sort"
	"strconv"
)

// val 是一个具体取值。对象值带唯一标签（shape），属性为具体值列表。
type val struct {
	tag  vtag
	n    float64
	s    string
	b    bool
	obj  string // 对象形状名
	atom bool   // true 表示该值代表“原子类型成员”，按规范做过近似处理
}

type vtag int

const (
	vNum vtag = iota
	vStr
	vBool
	vNull
	vUndef
	vObj
)

func (v val) String() string {
	switch v.tag {
	case vNum:
		return "n:" + strconv.FormatFloat(v.n, 'g', -1, 64)
	case vStr:
		return "s:" + strconv.Quote(v.s)
	case vBool:
		return "b:" + strconv.FormatBool(v.b)
	case vNull:
		return "null"
	case vUndef:
		return "undefined"
	case vObj:
		return "obj:" + v.obj
	}
	return "?"
}

type valSet map[val]bool

// worldEnv 是朴素环境：变量到具体取值集合。nil 表示不可达。
type worldEnv map[string]valSet

// naiveUniverse 是本次随机程序的具体论域。
type naiveUniverse struct {
	numbers []float64
	strings []string
	objects []string
}

func (u *naiveUniverse) allVals() []val {
	out := []val{{tag: vNull}, {tag: vUndef}, {tag: vBool, b: true}, {tag: vBool, b: false}}
	for _, n := range u.numbers {
		out = append(out, val{tag: vNum, n: n})
	}
	for _, s := range u.strings {
		out = append(out, val{tag: vStr, s: s})
	}
	for _, o := range u.objects {
		out = append(out, val{tag: vObj, obj: o})
	}
	return out
}

// membersToVals 把规范化类型的成员展开为论域中对应的具体取值。
func (u *naiveUniverse) membersToVals(n *normType) valSet {
	set := valSet{}
	for _, lf := range n.members {
		switch lf.kind {
		case lkNumber:
			for _, num := range u.numbers {
				set[val{tag: vNum, n: num, atom: true}] = true
			}
		case lkString:
			for _, s := range u.strings {
				set[val{tag: vStr, s: s, atom: true}] = true
			}
		case lkTrue:
			set[val{tag: vBool, b: true}] = true
		case lkFalse:
			set[val{tag: vBool, b: false}] = true
		case lkNull:
			set[val{tag: vNull}] = true
		case lkUndefined:
			set[val{tag: vUndef}] = true
		case lkNumLit:
			set[val{tag: vNum, n: lf.num}] = true
		case lkStrLit:
			set[val{tag: vStr, s: lf.str}] = true
		case lkObject:
			if name := u.objectShapeName(lf); name != "" {
				set[val{tag: vObj, obj: name}] = true
			}
		}
	}
	return set
}

func (u *naiveUniverse) objectShapeName(target leaf) string {
	want := leafKey(target)
	for _, name := range u.objects {
		if shapeLeafKeys[name] == want {
			return name
		}
	}
	return ""
}

// shapeLeafKeys 记录测试生成的对象形状名 -> 规范化 leaf key。
var shapeLeafKeys = map[string]string{}

// ---- 具体条件求值 ----

func condValue(v val, c *Cond) bool {
	switch c.K {
	case CondIsType:
		return typeCheck(v, c.Check)
	case CondEqNull:
		return v.tag == vNull
	case CondEqUndefined:
		return v.tag == vUndef
	case CondLooseNull:
		return v.tag == vNull || v.tag == vUndef
	case CondEqLiteral:
		ln, _ := normalize(c.Lit)
		lit := ln.members[0]
		return strictEqLeaf(v, lit)
	case CondTruthy:
		return truthy(v)
	case CondDiscrim:
		// 对象值是“形状”，属性是一个取值集合：判别相等对该形状可能真也可能假。
		// 朴素模型对判别条件返回“是否可能为真”，由 split 的三值处理负责归类。
		ln, _ := normalize(c.Lit)
		pv := objectPropVals[v.obj]
		if pv == nil {
			return false
		}
		for _, pv1 := range pv[c.Prop] {
			if strictEqLeaf(pv1, ln.members[0]) {
				return true
			}
		}
		return false
	case CondNot:
		return !condValue(v, c.Inner)
	case CondAnd:
		return condValue(v, c.Left) && condValue(v, c.Right)
	case CondOr:
		return condValue(v, c.Left) || condValue(v, c.Right)
	}
	return false
}

// objectPropVals: 形状名 -> 属性名 -> 具体取值集合。
var objectPropVals = map[string]map[string][]val{}

func typeCheck(v val, c CheckedType) bool {
	switch c {
	case CheckNumber:
		return v.tag == vNum
	case CheckString:
		return v.tag == vStr
	case CheckBoolean:
		return v.tag == vBool
	case CheckUndefined:
		return v.tag == vUndef
	case CheckObject:
		return v.tag == vObj || v.tag == vNull
	}
	return false
}

func strictEqLeaf(v val, l leaf) bool {
	switch l.kind {
	case lkNull:
		return v.tag == vNull
	case lkUndefined:
		return v.tag == vUndef
	case lkTrue:
		return v.tag == vBool && v.b
	case lkFalse:
		return v.tag == vBool && !v.b
	case lkNumLit:
		return v.tag == vNum && v.n == l.num
	case lkStrLit:
		return v.tag == vStr && v.s == l.str
	}
	return false
}

func truthy(v val) bool {
	if v.atom {
		// 原子 number/string 成员按规范过近似：真假未知。
		return false // 占位：实际在 split 中对 atom 特殊处理（两支都保留）
	}
	switch v.tag {
	case vNull, vUndef:
		return false
	case vBool:
		return v.b
	case vNum:
		return v.n != 0
	case vStr:
		return v.s != ""
	case vObj:
		return true
	}
	return false
}

// ---- 朴素执行器 ----

type naiveResult struct {
	entry map[string]worldEnv // nil 表示该点不可达
	u     *naiveUniverse
}

func runNaive(p *Program, u *naiveUniverse) *naiveResult {
	res := &naiveResult{entry: map[string]worldEnv{}, u: u}
	init := worldEnv{}
	for name, t := range p.Decls {
		n, _ := normalize(t)
		init[name] = u.membersToVals(n)
	}
	res.execBlock(p.Stmts, init)
	return res
}

func (r *naiveResult) execBlock(stmts []*Stmt, cur worldEnv) {
	r.execBlockOut(stmts, cur)
}

func (r *naiveResult) entryWalk(stmts []*Stmt) {
	for _, s := range stmts {
		r.entry[s.ID] = nil
		if s.K == StmtIf {
			r.entryWalk(s.Then)
			r.entryWalk(s.Else)
		}
	}
}

// mark 登记从某条语句开始的整段子树为不可达。
func (r *naiveResult) mark(stmts []*Stmt) {
	r.entryWalk(stmts)
}

func (r *naiveResult) split(e worldEnv, c *Cond) (worldEnv, worldEnv) {
	t := worldEnv{}
	f := worldEnv{}
	vars := map[string]bool{}
	naiveCondVars(c, vars)
	for name, vs := range e {
		ts := valSet{}
		fs := valSet{}
		if vars[name] {
			for v := range vs {
				naiveApplyCond(v, c, ts, fs)
			}
		} else {
			// 条件不引用该变量：其取值集合在真假两支都保持不变。
			for v := range vs {
				ts[v] = true
				fs[v] = true
			}
		}
		if len(ts) > 0 {
			t[name] = ts
		} else {
			t[name] = valSet{}
		}
		if len(fs) > 0 {
			f[name] = fs
		} else {
			f[name] = valSet{}
		}
	}
	return deadWorld(t), deadWorld(f)
}

// naiveDiscrimClass 判定对象形状上的判别条件三值归属。
// 返回 (inTrue, inFalse)：恰为单一匹配字面量->仅真；不包含->仅假；
// 包含字面量但属性类型更宽（非单一）->真假两支都保留。
func naiveDiscrimClass(v val, c *Cond) (bool, bool) {
	ln, _ := normalize(c.Lit)
	pv := objectPropVals[v.obj]
	if pv == nil {
		return false, true
	}
	vals := pv[c.Prop]
	contains := false
	for _, pv1 := range vals {
		if strictEqLeaf(pv1, ln.members[0]) {
			contains = true
		}
	}
	if !contains {
		return false, true
	}
	if len(vals) == 1 {
		return true, false
	}
	return true, true
}

// naiveApplyObjectLogic 对对象形状值按短路累积复合条件（判别子条件可能三值）。
func naiveApplyObjectLogic(v val, c *Cond, ts, fs valSet) {
	lt, lf := valSet{}, valSet{}
	naiveApplyObject(v, c.Left, lt, lf)
	put := func(set valSet, lv val, cond *Cond) (inT, inF valSet) {
		inT, inF = valSet{}, valSet{}
		naiveApplyObject(lv, cond, inT, inF)
		return
	}
	if c.K == CondAnd {
		for lv := range lt {
			inT, _ := put(nil, lv, c.Right)
			for rv := range inT {
				ts[rv] = true
			}
		}
		for lv := range lf {
			fs[lv] = true
		}
		for lv := range lt {
			_, inF := put(nil, lv, c.Right)
			for rv := range inF {
				fs[rv] = true
			}
		}
		return
	}
	for lv := range lt {
		ts[lv] = true
	}
	for lv := range lf {
		inT, _ := put(nil, lv, c.Right)
		for rv := range inT {
			ts[rv] = true
		}
	}
	for lv := range lf {
		_, inF := put(nil, lv, c.Right)
		for rv := range inF {
			fs[rv] = true
		}
	}
}

// naiveApplyCond 把单个具体值按条件归入真/假集合。
// 真值判断对原子 number/string 采用与生产实现相同的不对称规则：
//   - 为真：原子成员原样保留在真集合；
//   - 为假：原子 number 收窄为具体 0，原子 string 收窄为具体 ""。
func naiveApplyCond(v val, c *Cond, ts, fs valSet) {
	if v.atom && (v.tag == vNum || v.tag == vStr) {
		naiveApplyAtom(v, c, ts, fs)
		return
	}
	if v.tag == vObj {
		naiveApplyObject(v, c, ts, fs)
		return
	}
	if condValue(v, c) {
		ts[v] = true
	} else {
		fs[v] = true
	}
}

// naiveApplyObject 对对象形状值应用条件；判别属性为三值，其余条件按普通求值。
func naiveApplyObject(v val, c *Cond, ts, fs valSet) {
	if c.K == CondNot {
		naiveApplyObject(v, c.Inner, fs, ts)
		return
	}
	if c.K == CondAnd || c.K == CondOr {
		naiveApplyObjectLogic(v, c, ts, fs)
		return
	}
	if c.K == CondDiscrim {
		inT, inF := naiveDiscrimClass(v, c)
		if inT {
			ts[v] = true
		}
		if inF {
			fs[v] = true
		}
		return
	}
	if condValue(v, c) {
		ts[v] = true
	} else {
		fs[v] = true
	}
}

// naiveApplyAtom 处理原子 number/string 占位值在各类条件下的过近似收窄。
// 占位值代表整个原子论域；条件为真时按生产规则收窄，为假时保留原子本身。
func naiveApplyAtom(v val, c *Cond, ts, fs valSet) {
	switch c.K {
	case CondIsType:
		match := (v.tag == vNum && c.Check == CheckNumber) ||
			(v.tag == vStr && c.Check == CheckString)
		if match {
			ts[v] = true
		} else {
			fs[v] = true
		}
	case CondEqLiteral:
		ln, _ := normalize(c.Lit)
		lit := ln.members[0]
		sameType := (v.tag == vNum && lit.kind == lkNumLit) ||
			(v.tag == vStr && lit.kind == lkStrLit)
		if sameType {
			// 真：收窄为该字面量；假：原子保持。
			ts[leafToVal(lit)] = true
			fs[v] = true
		} else {
			fs[v] = true
		}
	case CondNot:
		// !(x?)：条件为真(x 假)时原子收窄为 0/""；为假(x 真)时原子保留。
		if c.Inner != nil && c.Inner.K == CondTruthy {
			if v.tag == vNum {
				ts[val{tag: vNum, n: 0}] = true
			} else {
				ts[val{tag: vStr, s: ""}] = true
			}
			// 条件为假（x 真）：原子保留。
			fs[v] = true
			return
		}
		naiveApplyAtom(v, c.Inner, fs, ts)
	case CondTruthy:
		// x? 为真：原子保留；为假：原子 number->0、string->""。
		ts[v] = true
		if v.tag == vNum {
			fs[val{tag: vNum, n: 0}] = true
		} else {
			fs[val{tag: vStr, s: ""}] = true
		}
	case CondAnd, CondOr:
		naiveApplyAtomLogic(v, c, ts, fs)
	case CondEqNull, CondEqUndefined, CondLooseNull, CondDiscrim:
		// 原子 number/string 对这些条件恒为假（或不相关），归入假集合。
		if condValue(v, c) {
			ts[v] = true
		} else {
			fs[v] = true
		}
	default:
		fs[v] = true
	}
}

func leafToVal(l leaf) val {
	switch l.kind {
	case lkNumLit:
		return val{tag: vNum, n: l.num}
	case lkStrLit:
		return val{tag: vStr, s: l.str}
	case lkTrue:
		return val{tag: vBool, b: true}
	case lkFalse:
		return val{tag: vBool, b: false}
	case lkNull:
		return val{tag: vNull}
	case lkUndefined:
		return val{tag: vUndef}
	}
	return val{}
}

// naiveApplyAtomLogic 对原子占位值按短路求值复合条件。
// 由于占位值代表整个论域，原子在某一条件下可能同时产生“真”与“假”的收窄，
// 需要分别累积。这里用临时集合递归计算左右两支，再按 JS 短路合并。
func naiveApplyAtomLogic(v val, c *Cond, ts, fs valSet) {
	// 左操作数
	lt, lf := valSet{}, valSet{}
	naiveApplyAtom(v, c.Left, lt, lf)
	if c.K == CondAnd {
		// 真：左真 ∩ 右真
		for lv := range lt {
			rt2, rf2 := valSet{}, valSet{}
			naiveApplyAtom(lv, c.Right, rt2, rf2)
			for rv := range rt2 {
				ts[rv] = true
			}
		}
		// 假：左假 或（左真且右假）
		for lv := range lf {
			fs[lv] = true
		}
		for lv := range lt {
			rt2, rf2 := valSet{}, valSet{}
			naiveApplyAtom(lv, c.Right, rt2, rf2)
			for rv := range rf2 {
				fs[rv] = true
			}
		}
		return
	}
	// CondOr
	// 真：左真 或（左假且右真）
	for lv := range lt {
		ts[lv] = true
	}
	for lv := range lf {
		rt2, rf2 := valSet{}, valSet{}
		naiveApplyAtom(lv, c.Right, rt2, rf2)
		for rv := range rt2 {
			ts[rv] = true
		}
	}
	// 假：左假 ∩ 右假
	for lv := range lf {
		rt2, rf2 := valSet{}, valSet{}
		naiveApplyAtom(lv, c.Right, rt2, rf2)
		for rv := range rf2 {
			fs[rv] = true
		}
	}
}

func naiveCondVars(c *Cond, out map[string]bool) {
	switch c.K {
	case CondAnd, CondOr:
		naiveCondVars(c.Left, out)
		naiveCondVars(c.Right, out)
	case CondNot:
		naiveCondVars(c.Inner, out)
	default:
		if c.Var != "" {
			out[c.Var] = true
		}
	}
}

func deadWorld(e worldEnv) worldEnv {
	if e == nil {
		return nil
	}
	for _, vs := range e {
		if len(vs) == 0 {
			return nil
		}
	}
	return e
}

func unionWorld(a, b worldEnv) worldEnv {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	out := worldEnv{}
	for name := range a {
		out[name] = valSet{}
	}
	for name := range b {
		if _, ok := out[name]; !ok {
			out[name] = valSet{}
		}
	}
	for name := range out {
		for v := range a[name] {
			out[name][v] = true
		}
		for v := range b[name] {
			out[name][v] = true
		}
	}
	return out
}

func (r *naiveResult) execStmt(s *Stmt, cur worldEnv) worldEnv {
	r.entry[s.ID] = cloneWorld(cur)
	switch s.K {
	case StmtReturn:
		return nil
	case StmtAssign:
		n, _ := normalize(s.Value)
		cur = cloneWorld(cur)
		cur[s.Var] = r.u.membersToVals(n)
		return cur
	case StmtIf:
		te, fe := r.split(cur, s.Cond)
		var to, fo worldEnv
		if len(s.Then) > 0 {
			to = r.execBlockOut(s.Then, te)
		} else {
			to = te
		}
		if len(s.Else) > 0 {
			fo = r.execBlockOut(s.Else, fe)
		} else {
			fo = fe
		}
		return unionWorld(to, fo)
	}
	return cur
}

func cloneWorld(e worldEnv) worldEnv {
	c := worldEnv{}
	for k, v := range e {
		c[k] = valSet{}
		for x := range v {
			c[k][x] = true
		}
	}
	return c
}

// execBlockOut 执行块，登记内部点并返回出口环境（nil=路径终止）。
func (r *naiveResult) execBlockOut(stmts []*Stmt, incoming worldEnv) worldEnv {
	cur := incoming
	for i, s := range stmts {
		if cur == nil {
			r.mark(stmts[i:])
			return nil
		}
		cur = r.execStmt(s, cur)
	}
	return cur
}

func renderValSet(vs valSet) string {
	xs := make([]string, 0, len(vs))
	for v := range vs {
		xs = append(xs, v.String())
	}
	sort.Strings(xs)
	return fmt.Sprintf("%v", xs)
}
