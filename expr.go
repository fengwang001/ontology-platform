package ontology

import (
	"fmt"
	"sort"
	"strings"
)

// CmpOp 是判定规则中支持的比较运算符。
type CmpOp int

const (
	OpEq CmpOp = iota
	OpNe
	OpLt
	OpLe
	OpGt
	OpGe
)

func (op CmpOp) String() string {
	switch op {
	case OpEq:
		return "=="
	case OpNe:
		return "!="
	case OpLt:
		return "<"
	case OpLe:
		return "<="
	case OpGt:
		return ">"
	case OpGe:
		return ">="
	}
	return "?"
}

// Expr 是标签判定规则的声明式表达式树。
// 表达式以属性取值与其他标签的判定结果为输入，输出布尔结论。
type Expr interface {
	// Attrs 返回该表达式直接引用的属性名集合。
	Attrs() map[string]struct{}
	// Tags 返回该表达式直接引用的标签名集合。
	Tags() map[string]struct{}
	String() string
	expr()
}

// ConstExpr 是常量。
type ConstExpr struct{ V Value }

// AttrExpr 引用当前实例的一个属性取值。
type AttrExpr struct{ Name string }

// TagExpr 引用同一对象类型上另一个标签的判定结果。
type TagExpr struct{ Tag string }

// NotExpr 对布尔子表达式取反。
type NotExpr struct{ X Expr }

// AndExpr 是若干布尔子表达式的合取。
type AndExpr struct{ Xs []Expr }

// OrExpr 是若干布尔子表达式的析取。
type OrExpr struct{ Xs []Expr }

// CmpExpr 对两个子表达式的取值进行比较，输出布尔值。
type CmpExpr struct {
	Op   CmpOp
	L, R Expr
}

func Const(v Value) Expr      { return ConstExpr{V: v} }
func Attr(name string) Expr   { return AttrExpr{Name: name} }
func TagRef(tag string) Expr  { return TagExpr{Tag: tag} }
func Not(x Expr) Expr         { return NotExpr{X: x} }
func And(xs ...Expr) Expr     { return AndExpr{Xs: xs} }
func Or(xs ...Expr) Expr      { return OrExpr{Xs: xs} }
func Cmp(op CmpOp, l, r Expr) Expr { return CmpExpr{Op: op, L: l, R: r} }

func (ConstExpr) expr() {}
func (AttrExpr) expr()  {}
func (TagExpr) expr()   {}
func (NotExpr) expr()   {}
func (AndExpr) expr()   {}
func (OrExpr) expr()    {}
func (CmpExpr) expr()   {}

func (e ConstExpr) Attrs() map[string]struct{} { return nil }
func (e AttrExpr) Attrs() map[string]struct{}  { return map[string]struct{}{e.Name: {}} }
func (e TagExpr) Attrs() map[string]struct{}   { return nil }
func (e NotExpr) Attrs() map[string]struct{}   { return e.X.Attrs() }
func (e AndExpr) Attrs() map[string]struct{}   { return unionAttrs(e.Xs) }
func (e OrExpr) Attrs() map[string]struct{}    { return unionAttrs(e.Xs) }
func (e CmpExpr) Attrs() map[string]struct{} {
	return mergeSets(e.L.Attrs(), e.R.Attrs())
}

func (e ConstExpr) Tags() map[string]struct{} { return nil }
func (e AttrExpr) Tags() map[string]struct{}  { return nil }
func (e TagExpr) Tags() map[string]struct{}   { return map[string]struct{}{e.Tag: {}} }
func (e NotExpr) Tags() map[string]struct{}   { return e.X.Tags() }
func (e AndExpr) Tags() map[string]struct{}   { return unionTags(e.Xs) }
func (e OrExpr) Tags() map[string]struct{}    { return unionTags(e.Xs) }
func (e CmpExpr) Tags() map[string]struct{} {
	return mergeSets(e.L.Tags(), e.R.Tags())
}

func unionAttrs(xs []Expr) map[string]struct{} {
	var out map[string]struct{}
	for _, x := range xs {
		out = mergeSets(out, x.Attrs())
	}
	return out
}

func unionTags(xs []Expr) map[string]struct{} {
	var out map[string]struct{}
	for _, x := range xs {
		out = mergeSets(out, x.Tags())
	}
	return out
}

func mergeSets(a, b map[string]struct{}) map[string]struct{} {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		out[k] = struct{}{}
	}
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}

func (e ConstExpr) String() string { return fmt.Sprintf("%v", e.V) }
func (e AttrExpr) String() string  { return "attr(" + e.Name + ")" }
func (e TagExpr) String() string   { return "tag(" + e.Tag + ")" }
func (e NotExpr) String() string   { return "not(" + e.X.String() + ")" }
func (e AndExpr) String() string {
	parts := make([]string, len(e.Xs))
	for i, x := range e.Xs {
		parts[i] = x.String()
	}
	return "and(" + strings.Join(parts, ", ") + ")"
}
func (e OrExpr) String() string {
	parts := make([]string, len(e.Xs))
	for i, x := range e.Xs {
		parts[i] = x.String()
	}
	return "or(" + strings.Join(parts, ", ")+")"
}
func (e CmpExpr) String() string {
	return "(" + e.L.String() + " " + e.Op.String() + " " + e.R.String() + ")"
}

// sortedKeys 以确定顺序返回集合元素，保证错误汇报与日志可复现。
func sortedKeys(s map[string]struct{}) []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
