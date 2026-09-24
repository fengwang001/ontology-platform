// Package predicate defines the filter predicate tree:
// constants, column comparisons, and boolean AND/OR/NOT.
package predicate

import "errors"

// Op is a comparison operator for column nodes.
type Op string

const (
	OpEq        Op = "="
	OpNeq       Op = "<>"
	OpLt        Op = "<"
	OpLte       Op = "<="
	OpGt        Op = ">"
	OpGte       Op = ">="
	OpIsNull    Op = "IS NULL"
	OpIsNotNull Op = "IS NOT NULL"
)

// Kind identifies a node's shape.
type Kind string

const (
	KindConst Kind = "const"
	KindCol   Kind = "col"
	KindAnd   Kind = "AND"
	KindOr    Kind = "OR"
	KindNot   Kind = "NOT"
)

var validOps = map[Op]bool{
	OpEq: true, OpNeq: true, OpLt: true, OpLte: true,
	OpGt: true, OpGte: true, OpIsNull: true, OpIsNotNull: true,
}

// Node is one predicate node.
//
// Const: Value is bool.
// Col:   Column is the referenced column; Op is its operator.
// And/Or: Children are the operands.
// Not:   Children[0] is the operand.
type Node struct {
	Kind     Kind
	Value    bool
	Column   string
	Op       Op
	Children []*Node
}

// Constructors keep call sites short in tests and the demo.
func Const(v bool) *Node { return &Node{Kind: KindConst, Value: v} }
func Col(col string, op Op) *Node {
	return &Node{Kind: KindCol, Column: col, Op: op}
}
func And(children ...*Node) *Node { return &Node{Kind: KindAnd, Children: children} }
func Or(children ...*Node) *Node  { return &Node{Kind: KindOr, Children: children} }
func Not(child *Node) *Node       { return &Node{Kind: KindNot, Children: []*Node{child}} }

// ErrInvalidPredicate marks a structurally invalid predicate tree.
var ErrInvalidPredicate = errors.New("invalid predicate")

// Validate walks the whole tree once and rejects malformed nodes:
// unknown kinds/operators, missing operands, or empty column names.
func Validate(n *Node) error {
	if n == nil {
		return ErrInvalidPredicate
	}
	switch n.Kind {
	case KindConst, KindCol:
		if n.Kind == KindCol {
			if n.Column == "" || !validOps[n.Op] {
				return ErrInvalidPredicate
			}
		}
	case KindAnd, KindOr:
		if len(n.Children) < 2 {
			return ErrInvalidPredicate
		}
		for _, c := range n.Children {
			if err := Validate(c); err != nil {
				return err
			}
		}
	case KindNot:
		if len(n.Children) != 1 || Validate(n.Children[0]) != nil {
			return ErrInvalidPredicate
		}
	default:
	return ErrInvalidPredicate
	}
	return nil
}

// Count returns the total number of nodes in the tree.
func Count(n *Node) int {
	if n == nil {
		return 0
	}
	total := 1
	for _, c := range n.Children {
		total += Count(c)
	}
	return total
}
