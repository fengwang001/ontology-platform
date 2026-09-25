// Package predicate defines the filter predicate tree used by the
// column-level permission filter.
package predicate

import "strconv"

// Kind identifies a predicate node type.
type Kind int

const (
	KindConst Kind = iota
	KindCmp
	KindAnd
	KindOr
	KindNot
)

// CmpOp is the operator of a column comparison leaf.
type CmpOp int

const (
	Eq CmpOp = iota
	Ne
	Lt
	Le
	Gt
	Ge
	IsNull
	IsNotNull
)

// Node is one node of a predicate tree.
//
// KindConst uses ConstValue. KindCmp uses Column, Op and Value
// (Value is nil for IsNull / IsNotNull). KindAnd and KindOr use
// Children (two or more). KindNot uses Children[0].
type Node struct {
	Kind       Kind
	ConstValue bool
	Column     string
	Op         CmpOp
	Value      any
	Children   []*Node
}

// Const builds a constant boolean node.
func Const(v bool) *Node {
	return &Node{Kind: KindConst, ConstValue: v}
}

// Cmp builds a column comparison leaf, e.g. Cmp("secret", Eq, 1)
// or Cmp("secret", IsNull, nil).
func Cmp(column string, op CmpOp, value any) *Node {
	return &Node{Kind: KindCmp, Column: column, Op: op, Value: value}
}

// Not builds a NOT node.
func Not(child *Node) *Node {
	return &Node{Kind: KindNot, Children: []*Node{child}}
}

// And builds an AND node over the given children.
func And(children ...*Node) *Node {
	return &Node{Kind: KindAnd, Children: children}
}

// Or builds an OR node over the given children.
func Or(children ...*Node) *Node {
	return &Node{Kind: KindOr, Children: children}
}

// Count returns the total number of nodes in the tree (nil counts as 0).
func Count(n *Node) int {
	if n == nil {
		return 0
	}
	total := 1
	for _, child := range n.Children {
		total += Count(child)
	}
	return total
}

// Path is the location of a node in the predicate tree: each element is
// the child index at that depth, root first. Empty path denotes the root.
type Path []int

// String renders the path as e.g. "[1,0]".
func (p Path) String() string {
	if len(p) == 0 {
		return "[]"
	}
	out := "["
	for i, idx := range p {
		if i > 0 {
			out += ","
		}
		out += strconv.Itoa(idx)
	}
	return out + "]"
}

// Equal reports whether two paths are identical.
func (p Path) Equal(other Path) bool {
	if len(p) != len(other) {
		return false
	}
	for i := range p {
		if p[i] != other[i] {
			return false
		}
	}
	return true
}
