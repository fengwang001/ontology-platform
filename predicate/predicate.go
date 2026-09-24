// Package predicate defines the filter predicate tree: column
// comparisons, boolean constants, and AND/OR/NOT junctions.
package predicate

import "fmt"

// Node is a predicate tree node. The interface is sealed: only this
// package's types implement it, so switches over Node are exhaustive.
type Node interface{ node() }

// Const is a boolean literal, e.g. the TRUE in `x = 1 OR TRUE`.
type Const struct{ Value bool }

// Compare is `Column = Value`.
type Compare struct {
	Column string
	Value  any
}

// IsNull is `Column IS NULL`.
type IsNull struct{ Column string }

// Not is `NOT Child`.
type Not struct{ Child Node }

// And is the conjunction of Children, evaluated left to right.
type And struct{ Children []Node }

// Or is the disjunction of Children, evaluated left to right.
type Or struct{ Children []Node }

func (Const) node()   {}
func (Compare) node() {}
func (IsNull) node()  {}
func (Not) node()     {}
func (And) node()     {}
func (Or) node()      {}

// Count returns the total number of nodes in the tree rooted at n.
// A nil node counts as zero (it is the absence of a predicate).
func Count(n Node) int {
	switch t := n.(type) {
	case nil:
		return 0
	case Const, Compare, IsNull:
		return 1
	case Not:
		return 1 + Count(t.Child)
	case And:
		return 1 + countAll(t.Children)
	case Or:
		return 1 + countAll(t.Children)
	default:
		panic(fmt.Sprintf("predicate: unknown node type %T", n))
	}
}

func countAll(kids []Node) int {
	total := 0
	for _, k := range kids {
		total += Count(k)
	}
	return total
}
