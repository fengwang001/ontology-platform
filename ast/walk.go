package ast

// notTri / andTri / orTri 为 Kleene 三值逻辑运算。
func notTri(t Tri) Tri {
	if t == TTrue {
		return TFalse
	} else if t == TFalse {
		return TTrue
	}
	return Unk
}

func andTri(a, b Tri) Tri {
	if a == TFalse || b == TFalse {
		return TFalse
	}
	if a == TTrue {
		return b
	}
	if b == TTrue {
		return a
	}
	return Unk
}

func orTri(a, b Tri) Tri {
	if a == TTrue || b == TTrue {
		return TTrue
	}
	if a == TFalse {
		return b
	}
	if b == TFalse {
		return a
	}
	return Unk
}

// ---- 构造辅助 ----

// BoolConst 构造常量真值节点。
func BoolConst(v Tri) Node { return Const{V: v} }

// ColRef 构造列操作数。
func ColRef(table, col string) Operand {
	return Operand{Ref: &Ref{Table: table, Col: col}}
}

// IntLit 构造非 NULL 整数操作数。
func IntLit(n int64) Operand { return Operand{Lit: n, IsLit: true} }

// NullLit 构造 NULL 操作数。
func NullLit() Operand { return Operand{IsLit: true, LitNull: true} }

// CmpNode 构造比较节点。
func CmpNode(op CmpOp, l, r Operand) Node { return Cmp{Op: op, L: l, R: r} }

// And 构造合取（单项表示退化 AND）。
func And(cs ...Node) Node { return Logic{Kind: "AND", Child: cs} }

// Or 构造析取（单项表示退化 OR）。
func Or(cs ...Node) Node { return Logic{Kind: "OR", Child: cs} }

// Not 构造否定。
func Not(c Node) Node { return Logic{Kind: "NOT", Child: []Node{c}} }

// ---- 迭代遍历（深度 1000 的链不溢出） ----

type frame struct {
	n Node
	i int
}

// Walk 以后序迭代遍历谓词树，对每个节点调用 f。
func Walk(root Node, f func(Node)) {
	st := []frame{{n: root}}
	for len(st) > 0 {
		top := &st[len(st)-1]
		if lg, ok := top.n.(Logic); ok && top.i < len(lg.Child) {
			child := lg.Child[top.i]
			top.i++
			st = append(st, frame{n: child})
			continue
		}
		f(top.n)
		st = st[:len(st)-1]
	}
}

// Count 返回节点数（迭代式）。
func Count(root Node) int {
	n := 0
	Walk(root, func(Node) { n++ })
	return n
}

func cmpRefs(c Cmp) []Ref {
	var rs []Ref
	if c.L.Ref != nil {
		rs = append(rs, *c.L.Ref)
	}
	if c.R.Ref != nil {
		rs = append(rs, *c.R.Ref)
	}
	return rs
}

// Refs 返回谓词引用的全部列。
func Refs(n Node) []Ref {
	var out []Ref
	Walk(n, func(x Node) {
		switch q := x.(type) {
		case Ref:
			out = append(out, q)
		case Cmp:
			out = append(out, cmpRefs(q)...)
		}
	})
	return out
}

// Tables 返回谓词引用的表集合。
func Tables(n Node) map[string]bool {
	m := map[string]bool{}
	for _, r := range Refs(n) {
		m[r.Table] = true
	}
	return m
}

// ---- 查询计划 ----

// PlanNode 为逻辑计划节点。
type PlanNode interface{ planNode() }

// Scan 为单表扫描，Filter 为下推到该表的谓词（nil 表示无）。
type Scan struct {
	Table  string
	Filter Node
}

// Join 为两计划的连接。
type Join struct{ L, R PlanNode }

// FilterNode 为留在顶层的过滤。
type FilterNode struct {
	In   PlanNode
	Pred Node
}

func (Scan) planNode()       {}
func (Join) planNode()       {}
func (FilterNode) planNode() {}

// PlanString 规范化打印计划。
func PlanString(p PlanNode) string {
	switch q := p.(type) {
	case Scan:
		if q.Filter == nil {
			return "Scan(" + q.Table + ")"
		}
		return "Scan(" + q.Table + ", " + q.Filter.String() + ")"
	case Join:
		return "Join(" + PlanString(q.L) + ", " + PlanString(q.R) + ")"
	case FilterNode:
		return "Filter(" + PlanString(q.In) + ", " + q.Pred.String() + ")"
	}
	return ""
}
