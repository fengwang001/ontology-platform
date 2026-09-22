package safeexpr

import "math/big"

// Node 是所有 AST 节点的接口。
type Node interface {
	node()
	// Pos 返回节点首字节位置（从 0 开始）。
	Pos() int
	// String 返回唯一的规范化 S 表达式，用于断言解析结构。
	String() string
}

// Number 为非负数字字面量，值按 big.Rat 精确解析。
type Number struct {
	PosVal int
	Value  *big.Rat
	Raw    string
}

func (*Number) node()      {}
func (n *Number) Pos() int { return n.PosVal }
func (n *Number) String() string {
	return n.Raw
}

// Unary 为一元负号：(- X)。
type Unary struct {
	OpPos int
	Child Node
}

func (*Unary) node()      {}
func (u *Unary) Pos() int { return u.OpPos }
func (u *Unary) String() string {
	return "(- " + u.Child.String() + ")"
}

// Binary 为二元运算：(op L R)，同级运算必须左结合。
type Binary struct {
	Op    tokenKind
	OpPos int
	Left  Node
	Right Node
}

func (*Binary) node()      {}
func (b *Binary) Pos() int { return b.Left.Pos() }
func (b *Binary) String() string {
	return "(" + b.opName() + " " + b.Left.String() + " " + b.Right.String() + ")"
}

func (b *Binary) opName() string {
	switch b.Op {
	case tokPlus:
		return "+"
	case tokMinus:
		return "-"
	case tokStar:
		return "*"
	case tokSlash:
		return "/"
	default:
		return "?"
	}
}
