// Package plan represents join plans and enumerates them with a
// subset-based dynamic program. Tie-breaking between equal-cost plans is
// deterministic: relative-epsilon cost equality, then the lexicographically
// smallest in-order leaf table-name sequence.
package plan

import (
	"math"

	"ontology/catalog"
)

// costEps is the relative tolerance for cost equality. It is ~5 orders of
// magnitude above the accumulated float64 rounding of any plan (<= 16
// tables), and far below any semantically meaningful cost difference given
// that the inputs are themselves estimates. See DESIGN.md.
const costEps = 1e-9

// Node is a plan tree node: a leaf scan (Table set, no children) or a
// binary join. Card and Cost are the estimated output cardinality and the
// cumulative cost of the subtree.
type Node struct {
	Table      string
	Left       *Node
	Right      *Node
	Predicates []catalog.Predicate
	Cartesian  bool
	Card       float64
	Cost       float64
	Unreliable bool
	Stale      bool
}

// Leaf reports whether n is a scan leaf.
func (n *Node) Leaf() bool { return n.Left == nil && n.Right == nil }

// Key returns the in-order leaf table-name sequence of the plan. It is the
// canonical tie-break key: equal-cost plans are ordered by it.
func (n *Node) Key() []string {
	if n.Leaf() {
		return []string{n.Table}
	}
	return append(n.Left.Key(), n.Right.Key()...)
}

// costEqual reports whether two costs are equal up to relative costEps.
func costEqual(a, b float64) bool {
	diff := math.Abs(a - b)
	scale := math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
	return diff <= costEps*scale
}

func lessKey(a, b []string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// better reports whether candidate a is preferred over incumbent b.
func better(a, b *Node) bool {
	if b == nil {
		return true
	}
	if !costEqual(a.Cost, b.Cost) {
		return a.Cost < b.Cost
	}
	return lessKey(a.Key(), b.Key())
}
