package predicate

import "errors"

var ErrInvalidPredicate = errors.New("invalid predicate")

type Op string

const (
	OpConst  Op = "const"
	OpEq     Op = "eq"
	OpIsNull Op = "is-null"
	OpAnd    Op = "and"
	OpOr     Op = "or"
	OpNot    Op = "not"
)

// Node is a constant, column comparison, or boolean predicate tree node.
type Node struct {
	Op     Op
	Bool   bool
	Column string
	Value  string
	Children []*Node
}

// Const returns a constant boolean predicate node.
func Const(value bool) *Node {
	return &Node{Op: OpConst, Bool: value}
}

// Eq returns a column equality-to-constant predicate node.
func Eq(column, value string) *Node {
	return &Node{Op: OpEq, Column: column, Value: value}
}

// IsNull returns a column null-test predicate node.
func IsNull(column string) *Node {
	return &Node{Op: OpIsNull, Column: column}
}

// And returns a conjunction node.
func And(children ...*Node) *Node {
	return &Node{Op: OpAnd, Children: children}
}

// Or returns a disjunction node.
func Or(children ...*Node) *Node {
	return &Node{Op: OpOr, Children: children}
}

// Not returns a negation node.
func Not(child *Node) *Node {
	return &Node{Op: OpNot, Children: []*Node{child}}
}

// Validate checks every node in predicate order and returns the first error.
func Validate(node *Node) error {
	if node == nil {
		return ErrInvalidPredicate
	}
	switch node.Op {
	case OpConst:
	case OpEq:
		if node.Column == "" || len(node.Children) != 0 {
			return ErrInvalidPredicate
		}
	case OpIsNull:
		if node.Column == "" || len(node.Children) != 0 {
			return ErrInvalidPredicate
		}
	case OpAnd, OpOr:
		if len(node.Children) == 0 {
			return ErrInvalidPredicate
		}
		for _, child := range node.Children {
			if err := Validate(child); err != nil {
				return err
			}
		}
	case OpNot:
		if len(node.Children) != 1 || node.Children[0] == nil {
			return ErrInvalidPredicate
		}
		if err := Validate(node.Children[0]); err != nil {
			return err
		}
	default:
		return ErrInvalidPredicate
	}
	return nil
}

// Count returns the total number of non-nil nodes in the tree.
func Count(node *Node) int {
	if node == nil {
		return 0
	}
	count := 1
	for _, child := range node.Children {
		count += Count(child)
	}
	return count
}

// PureConst reports whether the entire subtree contains only constant nodes.
func PureConst(node *Node) bool {
	if node == nil {
		return false
	}
	if node.Op == OpConst {
		return true
	}
	if node.Op != OpAnd && node.Op != OpOr && node.Op != OpNot {
		return false
	}
	if (node.Op == OpAnd || node.Op == OpOr) && len(node.Children) == 0 {
		return false
	}
	if node.Op == OpNot && len(node.Children) != 1 {
		return false
	}
	for _, child := range node.Children {
		if !PureConst(child) {
			return false
		}
	}
	return true
}

// EvalConst evaluates a subtree that PureConst accepted.
func EvalConst(node *Node) bool {
	switch node.Op {
	case OpConst:
		return node.Bool
	case OpNot:
		return !EvalConst(node.Children[0])
	case OpAnd:
		value := true
		for _, child := range node.Children {
			value = value && EvalConst(child)
		}
		return value
	case OpOr:
		value := false
		for _, child := range node.Children {
			value = value || EvalConst(child)
		}
		return value
	default:
		return false
	}
}
