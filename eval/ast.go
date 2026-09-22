package eval

import "strings"

// Node is an arithmetic expression parse-tree node.
type Node interface {
	// Pos returns the 0-based byte position where the node starts.
	Pos() int
	// String renders the canonical fully-parenthesized parse tree,
	// e.g. "((1-2)-3)", "(-(-3))".
	String() string
}

// NumberNode is a numeric literal kept verbatim from the source.
type NumberNode struct {
	Literal string
	pos     int
}

func (n *NumberNode) Pos() int       { return n.pos }
func (n *NumberNode) String() string { return n.Literal }

// UnaryNode is unary negation: -(Operand).
type UnaryNode struct {
	Operand Node
	pos     int
}

func (n *UnaryNode) Pos() int { return n.pos }
func (n *UnaryNode) String() string {
	var b strings.Builder
	b.WriteString("(-")
	b.WriteString(n.Operand.String())
	b.WriteByte(')')
	return b.String()
}

// BinaryNode is a binary operation: (Left Op Right).
type BinaryNode struct {
	Left  Node
	Right Node
	Op    byte
	pos   int
}

func (n *BinaryNode) Pos() int { return n.pos }
func (n *BinaryNode) String() string {
	var b strings.Builder
	b.WriteByte('(')
	b.WriteString(n.Left.String())
	b.WriteByte(n.Op)
	b.WriteString(n.Right.String())
	b.WriteByte(')')
	return b.String()
}
