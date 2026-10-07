package ontology

import "math"

// 本文件实现条件描述子（descriptor）表达式语言及其可满足性判定。
//
// 描述子是动作作者在声明条件时提供的静态元数据：它是条件通过的必要条件
// （即 Eval 通过 ⇒ 描述子成立）。引擎在动作定义阶段对同一阶段全部条件的
// 描述子合取做可满足性分析，若不可满足则声明自相矛盾，在注册期即被拒绝。
//
// 描述子只表达动作参数（Params）上的 int64 比较，因此可满足性是可判定的：
// 枚举所有原子命题的真值指派（2^a，a 为原子数），并检查每个指派在
// 同一参数字段上的区间一致性。复杂度只与条件声明自身的原子数相关，
// 与该动作历史上被调用的次数无关。

// CmpOp 是比较运算符。
type CmpOp int

const (
	CmpEq CmpOp = iota
	CmpNe
	CmpGt
	CmpGe
	CmpLt
	CmpLe
)

// Atom 是一个原子命题：参数字段与 int64 常量的比较。
type Atom struct {
	Field string
	Op    CmpOp
	Value int64
}

// ExprKind 是描述子表达式节点种类。
type ExprKind int

const (
	ExprAtom ExprKind = iota
	ExprAnd
	ExprOr
	ExprNot
)

// Expr 是描述子布尔表达式。
type Expr struct {
	Kind     ExprKind
	Atom     *Atom
	Children []*Expr
}

// AtomExpr 构造原子表达式。
func AtomExpr(field string, op CmpOp, value int64) *Expr {
	return &Expr{Kind: ExprAtom, Atom: &Atom{Field: field, Op: op, Value: value}}
}

// And 构造合取。
func And(children ...*Expr) *Expr { return &Expr{Kind: ExprAnd, Children: children} }

// Or 构造析取。
func Or(children ...*Expr) *Expr { return &Expr{Kind: ExprOr, Children: children} }

// Not 构造否定。
func Not(child *Expr) *Expr { return &Expr{Kind: ExprNot, Children: []*Expr{child}} }

// collectAtoms 收集表达式中的全部原子（按指针去重）。
func collectAtoms(e *Expr, acc *[]*Atom, seen map[*Atom]bool) {
	if e == nil {
		return
	}
	if e.Kind == ExprAtom {
		if e.Atom != nil && !seen[e.Atom] {
			seen[e.Atom] = true
			*acc = append(*acc, e.Atom)
		}
		return
	}
	for _, c := range e.Children {
		collectAtoms(c, acc, seen)
	}
}

// evalWith 在给定原子真值指派下求值。assign 以原子指针为键；
// 未出现在指派中的原子按 false 处理（调用方保证全覆盖）。
func (e *Expr) evalWith(assign map[*Atom]bool) bool {
	switch e.Kind {
	case ExprAtom:
		return assign[e.Atom]
	case ExprAnd:
		for _, c := range e.Children {
			if !c.evalWith(assign) {
				return false
			}
		}
		return true
	case ExprOr:
		for _, c := range e.Children {
			if c.evalWith(assign) {
				return true
			}
		}
		return false
	case ExprNot:
		return !e.Children[0].evalWith(assign)
	}
	return false
}

// maxSATAtoms 是描述子 SAT 分析的原子数上限。超过上限时放弃描述子分析
// （仅保留互斥检查），避免定义阶段出现指数爆炸；见设计文档。
const maxSATAtoms = 20

// fieldConstraint 是同一参数字段上若干原子真值指派合起来的数值约束。
type fieldConstraint struct {
	lo       int64
	loSet    bool
	loInc    bool
	hi       int64
	hiSet    bool
	hiInc    bool
	pins     map[int64]bool
	excludes map[int64]bool
}

func (c *fieldConstraint) addLower(v int64, inc bool) {
	if !c.loSet || v > c.lo || (v == c.lo && !inc) {
		c.lo, c.loSet, c.loInc = v, true, inc
	}
}

func (c *fieldConstraint) addUpper(v int64, inc bool) {
	if !c.hiSet || v < c.hi || (v == c.hi && !inc) {
		c.hi, c.hiSet, c.hiInc = v, true, inc
	}
}

// applyAtom 把“原子 a 取真值 truth”这一约束并入字段约束。
func (c *fieldConstraint) applyAtom(a *Atom, truth bool) {
	// 先把 (op, truth) 归约为等效的有效比较。
	op := a.Op
	if !truth {
		switch op {
		case CmpEq:
			op = CmpNe
		case CmpNe:
			op = CmpEq
		case CmpGt:
			op = CmpLe
		case CmpGe:
			op = CmpLt
		case CmpLt:
			op = CmpGe
		case CmpLe:
			op = CmpGt
		}
	}
	switch op {
	case CmpEq:
		c.pins[a.Value] = true
	case CmpNe:
		c.excludes[a.Value] = true
	case CmpGt:
		c.addLower(a.Value, false)
	case CmpGe:
		c.addLower(a.Value, true)
	case CmpLt:
		c.addUpper(a.Value, false)
	case CmpLe:
		c.addUpper(a.Value, true)
	}
}

// consistent 报告约束是否允许至少一个 int64 取值。
func (c *fieldConstraint) consistent() bool {
	if len(c.pins) > 1 {
		return false
	}
	for p := range c.pins {
		if c.excludes[p] {
			return false
		}
		if c.loSet {
			if p < c.lo || (p == c.lo && !c.loInc) {
				return false
			}
		}
		if c.hiSet {
			if p > c.hi || (p == c.hi && !c.hiInc) {
				return false
			}
		}
		return true
	}
	lo, hi := int64(math.MinInt64), int64(math.MaxInt64)
	if c.loSet {
		lo = c.lo
		if !c.loInc {
			if lo == math.MaxInt64 {
				return false
			}
			lo++
		}
	}
	if c.hiSet {
		hi = c.hi
		if !c.hiInc {
			if hi == math.MinInt64 {
				return false
			}
			hi--
		}
	}
	if lo > hi {
		return false
	}
	// 区间足够大时必有点未被排除；只有小区间需要精确核对排除集。
	if hi-lo < int64(len(c.excludes)) {
		for v := lo; ; v++ {
			if !c.excludes[v] {
				return true
			}
			if v == hi {
				return false
			}
		}
	}
	return true
}

// assignmentConsistent 检查一组原子真值指派在数值上是否自洽。
func assignmentConsistent(assign map[*Atom]bool) bool {
	byField := make(map[string]*fieldConstraint)
	for a, truth := range assign {
		fc, ok := byField[a.Field]
		if !ok {
			fc = &fieldConstraint{pins: map[int64]bool{}, excludes: map[int64]bool{}}
			byField[a.Field] = fc
		}
		fc.applyAtom(a, truth)
	}
	for _, fc := range byField {
		if !fc.consistent() {
			return false
		}
	}
	return true
}

// satResult 记录一次可满足性分析的统计信息，用于证明分析开销只与
// 条件声明本身相关。
type satResult struct {
	Satisfiable bool
	Atoms       int
	Assignments int64
	Skipped     bool // 原子数超过 maxSATAtoms 时放弃分析，按可满足处理
}

// conjunctionSAT 判定若干描述子的合取是否可满足。
func conjunctionSAT(exprs []*Expr) satResult {
	var atoms []*Atom
	seen := make(map[*Atom]bool)
	for _, e := range exprs {
		collectAtoms(e, &atoms, seen)
	}
	res := satResult{Atoms: len(atoms)}
	if len(atoms) > maxSATAtoms {
		res.Satisfiable = true
		res.Skipped = true
		return res
	}
	n := len(atoms)
	total := int64(1) << uint(n)
	assign := make(map[*Atom]bool, n)
	for mask := int64(0); mask < total; mask++ {
		res.Assignments++
		for i, a := range atoms {
			assign[a] = mask&(1<<uint(i)) != 0
		}
		if !assignmentConsistent(assign) {
			continue
		}
		ok := true
		for _, e := range exprs {
			if e != nil && !e.evalWith(assign) {
				ok = false
				break
			}
		}
		if ok {
			res.Satisfiable = true
			return res
		}
	}
	res.Satisfiable = false
	return res
}
