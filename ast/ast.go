// Package ast 定义表达式 AST 节点与结构相等判断，不依赖其他包。
package ast

// Kind 是节点类型标签；其数值即线上编码格式中的 1 字节标签，不得改动。
type Kind uint8

const (
	KIntLit  Kind = 0
	KBoolLit Kind = 1
	KVar     Kind = 2
	KNeg     Kind = 3
	KNot     Kind = 4
	KBinOp   Kind = 5
	KIf      Kind = 6
)

// Expr 是表达式节点。标量字段按 Kind 取用：
// KIntLit→Int，KBoolLit→Bool，KVar→Name，KBinOp→Op；
// Children 长度：Neg/Not=1 [child]，BinOp=2 [l,r]，If=3 [cond,then,else]。
type Expr struct {
	Kind     Kind
	Int      int64
	Bool     bool
	Name     string
	Op       string
	Children []*Expr
}

// 构造函数：codec/api 与测试、demo 统一经由它们构造合法 AST。

func IntLit(v int64) *Expr  { return &Expr{Kind: KIntLit, Int: v} }
func BoolLit(v bool) *Expr  { return &Expr{Kind: KBoolLit, Bool: v} }
func Var(name string) *Expr { return &Expr{Kind: KVar, Name: name} }
func Neg(c *Expr) *Expr     { return &Expr{Kind: KNeg, Children: []*Expr{c}} }
func Not(c *Expr) *Expr     { return &Expr{Kind: KNot, Children: []*Expr{c}} }
func BinOp(op string, l, r *Expr) *Expr {
	return &Expr{Kind: KBinOp, Op: op, Children: []*Expr{l, r}}
}
func If(cond, then, els *Expr) *Expr {
	return &Expr{Kind: KIf, Children: []*Expr{cond, then, els}}
}

// childCount 返回该 Kind 合法的子节点数，-1 表示未知 Kind。
func childCount(k Kind) int {
	switch k {
	case KNeg, KNot:
		return 1
	case KBinOp:
		return 2
	case KIf:
		return 3
	default:
		return -1
	}
}

// Equal 报告两棵 AST 是否结构相等：Kind/标量/Op/Name 逐字段相等
// （Name 按 Go 字符串语义即逐字节相等），子树递归相等。
func Equal(a, b *Expr) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind != b.Kind || a.Int != b.Int || a.Bool != b.Bool ||
		a.Name != b.Name || a.Op != b.Op {
		return false
	}
	if len(a.Children) != len(b.Children) {
		return false
	}
	for i := range a.Children {
		if !Equal(a.Children[i], b.Children[i]) {
			return false
		}
	}
	return true
}
