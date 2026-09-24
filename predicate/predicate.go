// Package predicate defines the filter predicate tree: column comparisons,
// AND, OR and NOT, plus constants and constant folding.
package predicate

import "errors"

// ErrInvalidPredicate is returned for malformed predicate trees.
var ErrInvalidPredicate = errors.New("predicate: invalid predicate")

// Op is a predicate node operator.
type Op int

const (
	Const  Op = iota // constant node, value in Bool
	Eq               // column equality, column in Column, literal in Value
	IsNull           // column IS NULL
	And
	Or
	Not
)

// Node is one predicate tree node.
type Node struct {
	Op     Op
	Column string
	Value  string
	Bool   bool
	Kids   []*Node
}

// NewConst builds a constant node.
func NewConst(v bool) *Node { return &Node{Op: Const, Bool: v} }

// NewEq builds a column = literal node.
func NewEq(col, val string) *Node { return &Node{Op: Eq, Column: col, Value: val} }

// NewIsNull builds a column IS NULL node.
func NewIsNull(col string) *Node { return &Node{Op: IsNull, Column: col} }

// NewAnd builds an AND node.
func NewAnd(kids ...*Node) *Node { return &Node{Op: And, Kids: kids} }

// NewOr builds an OR node.
func NewOr(kids ...*Node) *Node { return &Node{Op: Or, Kids: kids} }

// NewNot builds a NOT node.
func NewNot(kid *Node) *Node { return &Node{Op: Not, Kids: []*Node{kid}} }

// Validate checks structural well-formedness.
func Validate(n *Node) error {
	if n == nil {
		return nil
	}
	switch n.Op {
	case Const:
	case Eq, IsNull:
		if n.Column == "" {
			return ErrInvalidPredicate
		}
	case And, Or:
		if len(n.Kids) < 2 {
			return ErrInvalidPredicate
		}
	case Not:
		if len(n.Kids) != 1 {
			return ErrInvalidPredicate
		}
	default:
		return ErrInvalidPredicate
	}
	for _, kid := range n.Kids {
		if err := Validate(kid); err != nil {
			return err
		}
	}
	return nil
}

// Count returns the number of nodes in the tree.
func Count(n *Node) int {
	if n == nil {
		return 0
	}
	total := 1
	for _, kid := range n.Kids {
		total += Count(kid)
	}
	return total
}

// Fold constant-folds the tree. A nil predicate yields (nil, true, false).
// The booleans report whether the result is a constant and its value.
func Fold(n *Node) (folded *Node, isConst bool, val bool) {
	if n == nil {
		return nil, true, false
	}
	switch n.Op {
	case Const:
		return NewConst(n.Bool), true, n.Bool
	case And:
		return foldCombinator(n, false, func(a, b bool) bool { return a && b })
	case Or:
		return foldCombinator(n, true, func(a, b bool) bool { return a || b })
	case Not:
		kid, kConst, kVal := Fold(n.Kids[0])
		if kConst {
			return NewConst(!kVal), true, !kVal
		}
		return NewNot(kid), false, false
	default:
		return copyNode(n), false, false
	}
}

func foldCombinator(n *Node, neutral bool, combine func(bool, bool) bool) (*Node, bool, bool) {
	kids := make([]*Node, 0, len(n.Kids))
	acc := neutral
	for _, kid := range n.Kids {
		folded, isConst, val := Fold(kid)
		if isConst {
			absorbed := n.Op == And && val || n.Op == Or && !val
			if absorbed {
				continue
			}
			if n.Op == And && !val || n.Op == Or && val {
				return NewConst(val), true, val
			}
			kids = append(kids, NewConst(val))
			acc = combine(acc, val)
			continue
		}
		kids = append(kids, folded)
	}
	if len(kids) == 0 {
		return NewConst(acc), true, acc
	}
	if len(kids) == 1 {
		return kids[0], false, false
	}
	if n.Op == And {
		return NewAnd(kids...), false, false
	}
	return NewOr(kids...), false, false
}

func copyNode(n *Node) *Node {
	return &Node{Op: n.Op, Column: n.Column, Value: n.Value, Bool: n.Bool}
}
