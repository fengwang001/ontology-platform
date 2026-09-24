// Package predicate defines the filter predicate tree: column comparisons,
// boolean connectives and constant leaves.
package predicate

// Kind identifies a node type.
type Kind int

const (
	// Const is a boolean constant leaf.
	Const Kind = iota
	// Cmp is a column comparison leaf (Eq or IsNull).
	Cmp
	// Not is a one-child negation.
	Not
	// And is a zero-or-more child conjunction.
	And
	// Or is a zero-or-more child disjunction.
	Or
)

// CmpOp identifies a comparison performed on a column.
type CmpOp int

const (
	// Eq compares a column value with a string literal.
	Eq CmpOp = iota
	// IsNull tests whether the column value is NULL (absent from the row).
	IsNull
)

// Node is one node of the predicate tree.
type Node struct {
	Kind Kind
	// Bool holds the constant value for Kind == Const.
	Bool bool
	// Col, Op and Lit describe a Cmp node.
	Col string
	Op  CmpOp
	Lit string
	// Children holds the operands of Not/And/Or.
	Children []*Node
}

// BoolConst builds a constant leaf.
func BoolConst(v bool) *Node { return &Node{Kind: Const, Bool: v} }

// Equal builds a "col = lit" comparison.
func Equal(col, lit string) *Node {
	return &Node{Kind: Cmp, Col: col, Op: Eq, Lit: lit}
}

// NullTest builds a "col IS NULL" comparison.
func NullTest(col string) *Node {
	return &Node{Kind: Cmp, Col: col, Op: IsNull}
}

// Not builds a negation.
func Not(child *Node) *Node { return &Node{Kind: Not, Children: []*Node{child}} }

// And builds a conjunction.
func And(children ...*Node) *Node { return &Node{Kind: And, Children: children} }

// Or builds a disjunction.
func Or(children ...*Node) *Node { return &Node{Kind: Or, Children: children} }

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

// Eval evaluates the predicate against a row. A missing column reads as NULL:
// its comparisons are false, IsNull is true. It must only be called on a
// predicate that has passed a visibility compile, so no hidden column is read.
func Eval(n *Node, row map[string]string) bool {
	switch n.Kind {
	case Const:
		return n.Bool
	case Cmp:
		v, ok := row[n.Col]
		switch n.Op {
		case Eq:
			return ok && v == n.Lit
		case IsNull:
			return !ok
		}
	case Not:
		return !Eval(n.Children[0], row)
	case And:
		for _, c := range n.Children {
			if !Eval(c, row) {
				return false
			}
		}
		return true
	case Or:
		for _, c := range n.Children {
			if Eval(c, row) {
				return true
			}
		}
		return false
	}
	return false
}
