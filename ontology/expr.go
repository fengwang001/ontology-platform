package ontology

import "fmt"

// Value 是属性取值。支持的动态类型：bool、string、int64、float64。
type Value = any

// Expr 是标签判定规则的表达式树。
// 实现：Attr、TagRef、Const、BinOp、Not。
type Expr interface{ exprNode() }

// Attr 引用当前实例某个属性的真实取值。
type Attr struct{ Name string }

// TagRef 引用同一实例上另一个标签的携带状态（bool）。
type TagRef struct{ Tag string }

// Const 是常量。
type Const struct{ V Value }

// Op 是二元运算符。
type Op int

const (
	OpEq Op = iota
	OpNe
	OpLt
	OpLe
	OpGt
	OpGe
	OpAnd
	OpOr
)

// BinOp 是二元运算。And/Or 不短路：两侧始终求值，
// 保证求值路径不依赖属性取值（防推断泄漏 + 开销可静态界定）。
type BinOp struct {
	Op   Op
	L, R Expr
}

// Not 是逻辑非。
type Not struct{ X Expr }

func (Attr) exprNode()   {}
func (TagRef) exprNode() {}
func (Const) exprNode()  {}
func (BinOp) exprNode()  {}
func (Not) exprNode()    {}

// attrResolver 按属性名取真实取值；name 保证已在 schema 中登记。
type attrResolver func(name string) Value

// tagResolver 返回同一实例上另一标签的携带状态。
type tagResolver func(tag string) (bool, error)

// eval 对表达式求值。类型不匹配返回普通错误（不含属性取值，防泄漏）。
func eval(e Expr, attrs attrResolver, tags tagResolver) (Value, error) {
	switch n := e.(type) {
	case Const:
		return n.V, nil
	case Attr:
		return attrs(n.Name), nil
	case TagRef:
		return tags(n.Tag)
	case Not:
		v, err := eval(n.X, attrs, tags)
		if err != nil {
			return nil, err
		}
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("NOT 的操作数不是布尔类型")
		}
		return !b, nil
	case BinOp:
		l, err := eval(n.L, attrs, tags)
		if err != nil {
			return nil, err
		}
		r, err := eval(n.R, attrs, tags)
		if err != nil {
			return nil, err
		}
		return applyBinOp(n.Op, l, r)
	default:
		return nil, fmt.Errorf("未知表达式节点 %T", e)
	}
}

func applyBinOp(op Op, l, r Value) (Value, error) {
	switch op {
	case OpAnd, OpOr:
		lb, lok := l.(bool)
		rb, rok := r.(bool)
		if !lok || !rok {
			return nil, fmt.Errorf("逻辑运算的操作数不是布尔类型")
		}
		if op == OpAnd {
			return lb && rb, nil
		}
		return lb || rb, nil
	case OpEq:
		return valuesEqual(l, r), nil
	case OpNe:
		return !valuesEqual(l, r), nil
	}
	c, err := compareOrdered(l, r)
	if err != nil {
		return nil, err
	}
	switch op {
	case OpLt:
		return c < 0, nil
	case OpLe:
		return c <= 0, nil
	case OpGt:
		return c > 0, nil
	case OpGe:
		return c >= 0, nil
	}
	return nil, fmt.Errorf("未知运算符 %d", int(op))
}

func valuesEqual(l, r Value) bool {
	switch lv := l.(type) {
	case int64:
		if rv, ok := r.(float64); ok {
			return float64(lv) == rv
		}
	case float64:
		if rv, ok := r.(int64); ok {
			return lv == float64(rv)
		}
	}
	return l == r
}

func compareOrdered(l, r Value) (int, error) {
	toFloat := func(v Value) (float64, bool) {
		switch n := v.(type) {
		case int64:
			return float64(n), true
		case float64:
			return n, true
		}
		return 0, false
	}
	if lf, ok := toFloat(l); ok {
		if rf, ok := toFloat(r); ok {
			switch {
			case lf < rf:
				return -1, nil
			case lf > rf:
				return 1, nil
			}
			return 0, nil
		}
	}
	if ls, ok := l.(string); ok {
		if rs, ok := r.(string); ok {
			switch {
			case ls < rs:
				return -1, nil
			case ls > rs:
				return 1, nil
			}
			return 0, nil
		}
	}
	return 0, fmt.Errorf("不可比较的操作数类型")
}

// collectRefs 静态收集表达式引用的属性名与被引用的标签名。
func collectRefs(e Expr, attrs map[string]struct{}, tags map[string]struct{}) {
	switch n := e.(type) {
	case Attr:
		attrs[n.Name] = struct{}{}
	case TagRef:
		tags[n.Tag] = struct{}{}
	case BinOp:
		collectRefs(n.L, attrs, tags)
		collectRefs(n.R, attrs, tags)
	case Not:
		collectRefs(n.X, attrs, tags)
	}
}
