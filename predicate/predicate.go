// Package predicate defines a filter predicate tree: column comparisons
// combined with AND, OR, NOT, plus boolean constants for folding.
package predicate

// Op is a comparison operator between a column and a literal value.
type Op int

const (
	Eq Op = iota
	Ne
	Lt
	Le
	Gt
	Ge
	IsNull
	IsNotNull
)

func (o Op) String() string {
	switch o {
	case Eq:
		return "="
	case Ne:
		return "!="
	case Lt:
		return "<"
	case Le:
		return "<="
	case Gt:
		return ">"
	case Ge:
		return ">="
	case IsNull:
		return "IS NULL"
	case IsNotNull:
		return "IS NOT NULL"
	}
	return "?"
}

// Node is a predicate tree node. A nil Node means "no filter".
type Node interface{ node() }

// Const is a boolean literal, produced by or consumed by Fold.
type Const struct{ Value bool }

// Cmp compares a column against a literal value (ignored for IsNull/
// IsNotNull).
type Cmp struct {
	Column string
	Op     Op
	Value  any
}

// And is the conjunction of L and R.
type And struct{ L, R Node }

// Or is the disjunction of L and R.
type Or struct{ L, R Node }

// Not negates X.
type Not struct{ X Node }

func (Const) node() {}
func (Cmp) node()   {}
func (And) node()   {}
func (Or) node()    {}
func (Not) node()   {}

// Count returns the total number of nodes in the tree rooted at n.
func Count(n Node) int {
	switch t := n.(type) {
	case Const, Cmp:
		return 1
	case And:
		return 1 + Count(t.L) + Count(t.R)
	case Or:
		return 1 + Count(t.L) + Count(t.R)
	case Not:
		return 1 + Count(t.X)
	}
	return 0
}

// Fold performs constant folding: boolean constants are propagated
// through And/Or/Not, and provably dead branches are eliminated
// (e.g. Or(TRUE, x) -> TRUE, And(FALSE, x) -> FALSE). Comparisons
// are never folded, so a column surviving Fold is genuinely read.
func Fold(n Node) Node {
	switch t := n.(type) {
	case And:
		l, r := Fold(t.L), Fold(t.R)
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
		l, r := Fold(t.L), Fold(t.R)
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
	case Not:
		x := Fold(t.X)
		if c, ok := x.(Const); ok {
			return Const{!c.Value}
		}
		return Not{x}
	default:
		return n
	}
}
