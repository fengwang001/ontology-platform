package ontology

import (
	"fmt"
	"math/big"
)

// Node 是解析树的节点。同一表达式字符串只对应唯一一棵树，
// String 输出全括号形式，可直接用于结构断言。
type Node interface {
	// String 返回全括号规范形式，如 "((1 - 2) - 3)"。
	String() string
}

// Lit 是数值字面量，保存精确的 big.Rat 值。
type Lit struct {
	Text string // 原始文本（只读，未做改写）
	Pos  int    // 字面量起始字节位置
	rat  *big.Rat
}

// String 返回字面量原始文本。
func (l *Lit) String() string { return l.Text }

// Unary 是一元负号节点。
type Unary struct {
	X   Node
	Pos int // '-' 的字节位置
}

// String 返回形如 "(-X)" 的规范形式。
func (u *Unary) String() string { return "(-" + u.X.String() + ")" }

// Binary 是二元运算节点，Op 为 '+'、'-'、'*'、'/' 之一。
type Binary struct {
	Op   byte
	L, R Node
	Pos  int // 运算符的字节位置
}

// String 返回形如 "(L op R)" 的规范形式。
func (b *Binary) String() string {
	return fmt.Sprintf("(%s %c %s)", b.L, b.Op, b.R)
}
