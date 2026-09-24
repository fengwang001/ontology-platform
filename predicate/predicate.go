// Package predicate models boolean filter predicates as an immutable tree.
package predicate

// Pred is a predicate tree node.
type Pred interface{ predNode() }

// Const is a boolean literal, used for constant-only predicates and folding.
type Const struct{ Value bool }

// Compare is Column = Value.
type Compare struct {
	Column string
	Value  string
}

// IsNull is Column IS NULL.
type IsNull struct{ Column string }

// Not negates Inner.
type Not struct{ Inner Pred }

// And is L && R.
type And struct{ L, R Pred }

// Or is L || R.
type Or struct{ L, R Pred }

func (Const) predNode()   {}
func (Compare) predNode() {}
func (IsNull) predNode()  {}
func (Not) predNode()     {}
func (And) predNode()     {}
func (Or) predNode()      {}

// NodeCount returns the total number of nodes in the tree. A nil Pred
// (no filter) counts as zero.
func NodeCount(p Pred) int {
	switch n := p.(type) {
	case Const, Compare, IsNull:
		return 1
	case Not:
		return 1 + NodeCount(n.Inner)
	case And:
		return 1 + NodeCount(n.L) + NodeCount(n.R)
	case Or:
		return 1 + NodeCount(n.L) + NodeCount(n.R)
	}
	return 0
}

// Fold applies constant folding: NOT of a constant is negated, AND/OR absorb
// or drop constant branches. Column comparisons are never folded. The result
// is the tree that evaluation would actually execute.
func Fold(p Pred) Pred {
	switch n := p.(type) {
	case Not:
		inner := Fold(n.Inner)
		if c, ok := inner.(Const); ok {
			return Const{!c.Value}
		}
		return Not{inner}
	case And:
		l, r := Fold(n.L), Fold(n.R)
		if c, ok := l.(Const); ok {
			if !c.Value {
				return Const{false}
			}
			return r
		}
		if c, ok := r.(Const); ok {
			if !c.Value {
				return Const{false}
			}
			return l
		}
		return And{l, r}
	case Or:
		l, r := Fold(n.L), Fold(n.R)
		if c, ok := l.(Const); ok {
			if c.Value {
				return Const{true}
			}
			return r
		}
		if c, ok := r.(Const); ok {
			if c.Value {
				return Const{true}
			}
			return l
		}
		return Or{l, r}
	}
	return p
}
