// Package predicate 定义过滤谓词树：列比较、AND、OR、NOT，以及纯函数式的折叠与求值。
package predicate

// Op 是叶子比较的运算符。
type Op string

const (
	OpEq      Op = "="
	OpNeq     Op = "!="
	OpLt      Op = "<"
	OpLeq     Op = "<="
	OpGt      Op = ">"
	OpGeq     Op = ">="
	OpIsNull  Op = "IS NULL"
)

// Node 是谓词树节点。
type Node interface{ isNode() }

// Const 是布尔常量。
type Const struct{ Value bool }

// Cmp 是列与值（或 NULL）的叶子比较；IsNull 时 Value 必须为 nil。
type Cmp struct {
	Col   string
	Op    Op
	Value any
}

// Not 是逻辑非。
type Not struct{ X Node }

// And 是逻辑与（至少一个子节点）。
type And struct{ Xs []Node }

// Or 是逻辑或（至少一个子节点）。
type Or struct{ Xs []Node }

func (Const) isNode() {}
func (Cmp) isNode()   {}
func (Not) isNode()   {}
func (And) isNode()   {}
func (Or) isNode()    {}

// Count 返回谓词树的节点总数；nil 视为空谓词，返回 0。
func Count(n Node) int {
	switch t := n.(type) {
	case nil:
		return 0
	case Const, Cmp:
		return 1
	case Not:
		return 1 + Count(t.X)
	case And:
		c := 1
		for _, x := range t.Xs {
			c += Count(x)
		}
		return c
	case Or:
		c := 1
		for _, x := range t.Xs {
			c += Count(x)
		}
		return c
	default:
		return 0
	}
}

// Fold 做常量折叠：纯常量子树折叠为 Const；AND/OR 吸收恒真/恒假支。
// 返回的 ok=true 表示整棵子树折叠为常量。nil 视为恒真空谓词。
func Fold(n Node) (Node, bool) {
	switch t := n.(type) {
	case nil:
		return Const{Value: true}, true
	case Const:
		return t, true
	case Cmp:
		return t, false
	case Not:
		x, ok := Fold(t.X)
		if !ok {
			return Not{X: x}, false
		}
		return Const{Value: !x.(Const).Value}, true
	case And:
		return foldJunction(t.Xs, true, false, true)
	case Or:
		return foldJunction(t.Xs, false, true, false)
	default:
		return n, false
	}
}

func foldJunction(xs []Node, neutral, absorbing, isAnd bool) (Node, bool) {
	live := make([]Node, 0, len(xs))
	for _, x := range xs {
		f, ok := Fold(x)
		if !ok {
			live = append(live, f)
			continue
		}
		if f.(Const).Value == absorbing {
			return Const{Value: absorbing}, true
		}
	}
	if len(live) == 0 {
		return Const{Value: neutral}, true
	}
	if isAnd {
		return And{Xs: live}, false
	}
	return Or{Xs: live}, false
}

// Eval 针对单行求值谓词；nil 谓词恒真。仅使用行中已有的列，
// 缺列按 NULL 参与比较（调用方需先通过 filter 的可见性检查）。
func Eval(n Node, row map[string]any) bool {
	f, ok := Fold(n)
	if ok {
		return f.(Const).Value
	}
	return eval(f, row)
}

func eval(n Node, row map[string]any) bool {
	switch t := n.(type) {
	case Const:
		return t.Value
	case Cmp:
		return compare(t, row)
	case Not:
		return !eval(t.X, row)
	case And:
		for _, x := range t.Xs {
			if !eval(x, row) {
				return false
			}
		}
		return true
	case Or:
		for _, x := range t.Xs {
			if eval(x, row) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

func compare(c Cmp, row map[string]any) bool {
	v, present := row[c.Col]
	if c.Op == OpIsNull {
		return !present || v == nil
	}
	if !present || v == nil {
		return c.Op == OpNeq // 二值化：NULL 仅在 != 下放行（求值阶段前，权限检查已完成）
	}
	switch c.Op {
	case OpEq:
		return valuesEqual(v, c.Value)
	case OpNeq:
		return !valuesEqual(v, c.Value)
	}
	return compareOrdered(v, c.Value, c.Op)
}

func valuesEqual(a, b any) bool {
	if an, aok := number(a); aok {
		if bn, bok := number(b); bok {
			return an == bn
		}
	}
	return a == b
}

func compareOrdered(a, b any, op Op) bool {
	an, aok := number(a)
	bn, bok := number(b)
	if !aok || !bok {
		as, aok2 := a.(string)
		bs, bok2 := b.(string)
		if !aok2 || !bok2 {
			return false
		}
		return orderOrdered(as, bs, op)
	}
	return orderOrdered(an, bn, op)
}

func orderOrdered[T int64 | float64 | string](a, b T, op Op) bool {
	switch op {
	case OpLt:
		return a < b
	case OpLeq:
		return a <= b
	case OpGt:
		return a > b
	case OpGeq:
		return a >= b
	default:
		return false
	}
}

func number(v any) (float64, bool) {
	switch t := v.(type) {
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case float64:
		return t, true
	case float32:
		return float64(t), true
	default:
		return 0, false
	}
}
