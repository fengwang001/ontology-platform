// Package predicate defines the filter predicate tree: column comparisons
// combined with AND, OR, NOT, plus constant folding.
package predicate

// Node is a predicate tree node. Implemented by Const, Cmp, Not, And, Or.
type Node interface{ node() }

// Const is a constant boolean, e.g. the folded result of "1 = 1".
type Const struct{ Value bool }

// Op is a comparison operator.
type Op int

const (
	OpEq     Op = iota // column = value
	OpIsNull           // column IS NULL
)

// Cmp compares a column against a literal value.
type Cmp struct {
	Column string
	Op     Op
	Value  any
}

// Not negates its child.
type Not struct{ X Node }

// And conjoins two children.
type And struct{ L, R Node }

// Or disjoins two children.
type Or struct{ L, R Node }

func (Const) node() {}
func (Cmp) node()   {}
func (Not) node()   {}
func (And) node()   {}
func (Or) node()    {}

// Eq builds column = value.
func Eq(column string, value any) Cmp { return Cmp{Column: column, Op: OpEq, Value: value} }

// IsNull builds column IS NULL.
func IsNull(column string) Cmp { return Cmp{Column: column, Op: OpIsNull} }

// Bool builds a constant, e.g. the already-known result of "1 = 1".
func Bool(v bool) Const { return Const{Value: v} }

// Count returns the total number of nodes in the tree.
func Count(n Node) int {
	switch t := n.(type) {
	case Const, Cmp:
		return 1
	case Not:
		return 1 + Count(t.X)
	case And:
		return 1 + Count(t.L) + Count(t.R)
	case Or:
		return 1 + Count(t.L) + Count(t.R)
	}
	return 0
}

// Fold constant-folds the tree. A constant-only subtree is evaluated; an OR
// with a constant-true branch collapses to true (the other branch, including
// any column references it holds, is discarded and never read); an OR with a
// constant-false branch drops that branch. AND is dual; NOT of a constant is
// negated. Folding is semantics-preserving.
func Fold(n Node) Node {
	switch t := n.(type) {
	case Const, Cmp:
		return t
	case Not:
		x := Fold(t.X)
		if c, ok := x.(Const); ok {
			return Const{!c.Value}
		}
		return Not{x}
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
	}
	return n
}
