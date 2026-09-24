// Package predicate defines filter predicate trees: column comparisons,
// IS NULL probes, boolean constants, and AND/OR/NOT junctions.
package predicate

// Kind identifies the node type of a predicate tree.
type Kind int

const (
	Const  Kind = iota // boolean constant, Val holds a bool
	Eq                 // Col = Val
	IsNull             // Col IS NULL
	And                // conjunction of Kids
	Or                 // disjunction of Kids
	Not                // negation of Kid
)

// Node is one node of a predicate tree. Build trees with the constructors
// below; do not mix fields across kinds.
type Node struct {
	Kind Kind
	Col  string  // Eq, IsNull: referenced column
	Val  any     // Const: bool; Eq: comparison value
	Kids []*Node // And, Or
	Kid  *Node   // Not
}

// ConstBool returns a boolean constant node.
func ConstBool(b bool) *Node { return &Node{Kind: Const, Val: b} }

// Equal returns a Col = val comparison node.
func Equal(col string, val any) *Node { return &Node{Kind: Eq, Col: col, Val: val} }

// IsNullOf returns a Col IS NULL node.
func IsNullOf(col string) *Node { return &Node{Kind: IsNull, Col: col} }

// AndOf returns a conjunction node.
func AndOf(kids ...*Node) *Node { return &Node{Kind: And, Kids: kids} }

// OrOf returns a disjunction node.
func OrOf(kids ...*Node) *Node { return &Node{Kind: Or, Kids: kids} }

// NotOf returns a negation node.
func NotOf(kid *Node) *Node { return &Node{Kind: Not, Kid: kid} }

// Count returns the total number of nodes in the tree rooted at n.
func Count(n *Node) int {
	if n == nil {
		return 0
	}
	total := 1
	switch n.Kind {
	case And, Or:
		for _, k := range n.Kids {
			total += Count(k)
		}
	case Not:
		total += Count(n.Kid)
	}
	return total
}

// Fold rewrites n by constant folding, preserving semantics for every row.
// Subtrees eliminated by folding are considered unread by the permission
// filter (see DESIGN.md section 2). Fold returns a new tree; n is untouched.
func Fold(n *Node) *Node {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case Const, Eq, IsNull:
		return n
	case Not:
		kid := Fold(n.Kid)
		if kid.Kind == Const {
			return ConstBool(!kid.Val.(bool))
		}
		return NotOf(kid)
	case And:
		return foldJunction(n.Kids, true)
	case Or:
		return foldJunction(n.Kids, false)
	}
	return n
}

// foldJunction folds an AND (conj=true) or OR (conj=false) node.
// For AND: any false child short-circuits to false, true children drop out.
// For OR: any true child short-circuits to true, false children drop out.
func foldJunction(kids []*Node, conj bool) *Node {
	kept := make([]*Node, 0, len(kids))
	for _, raw := range kids {
		kid := Fold(raw)
		if kid.Kind == Const {
			if kid.Val.(bool) != conj {
				return ConstBool(!conj) // short-circuit: whole junction decided
			}
			continue // neutral element, drop
		}
		kept = append(kept, kid)
	}
	switch len(kept) {
	case 0:
		return ConstBool(conj)
	case 1:
		return kept[0]
	}
	if conj {
		return AndOf(kept...)
	}
	return OrOf(kept...)
}
