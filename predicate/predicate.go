// Package predicate defines the filter predicate tree: column comparisons,
// boolean constants, AND/OR/NOT, and evaluation over projected rows.
package predicate

import "errors"

// Op identifies a predicate node kind.
type Op int

const (
	Eq Op = iota // column = literal
	IsNull       // column IS NULL
	Not
	And
	Or
	Const // boolean constant; Literal is 0 (false) or 1 (true)
)

// ErrEvalMissingColumn means evaluation read a column absent from the row.
// The filter only ever evaluates predicates after visibility enforcement, so
// this signals an internal/programmer error rather than a policy denial.
var ErrEvalMissingColumn = errors.New("predicate: column missing from row")

// Value is a nullable column value. Absence of NULL and presence of V means
// the value is V. Null distinguishes "value is NULL" from a missing key.
type Value struct {
	V    string
	Null bool
}

// Row is a projected row: visible keys only.
type Row map[string]Value

// Node is a predicate tree node.
type Node struct {
	Op      Op
	Column  string // Eq, IsNull
	Literal string // Eq
	Value   bool   // Const
	Children []*Node
}

// Count returns the total number of nodes in the tree.
func (n *Node) Count() int {
	if n == nil {
		return 0
	}
	total := 1
	for _, child := range n.Children {
		total += child.Count()
	}
	return total
}

// Eval evaluates the predicate against a row with SQL-like three-valued
// semantics represented by (result, unknown): unknown==true means UNKNOWN.
func Eval(n *Node, row Row) (bool, bool, error) {
	switch n.Op {
	case Const:
		return n.Value, false, nil
	case IsNull:
		v, ok := row[n.Column]
		if !ok {
			return false, false, ErrEvalMissingColumn
		}
		return v.Null, false, nil
	case Eq:
		v, ok := row[n.Column]
		if !ok {
			return false, false, ErrEvalMissingColumn
		}
		if v.Null {
			return false, true, nil // NULL = x -> UNKNOWN
		}
		return v.V == n.Literal, false, nil
	case Not:
		b, u, err := Eval(n.Children[0], row)
		if err != nil {
			return false, false, err
		}
		return !b, u, nil // NOT UNKNOWN -> UNKNOWN
	case And:
		anyUnknown := false
		for _, child := range n.Children {
			b, u, err := Eval(child, row)
			if err != nil {
				return false, false, err
			}
			if u {
				anyUnknown = true
				continue
			}
			if !b {
				return false, false, nil // false dominates
			}
		}
		if anyUnknown {
			return false, true, nil
		}
		return true, false, nil
	case Or:
		anyUnknown := false
		for _, child := range n.Children {
			b, u, err := Eval(child, row)
			if err != nil {
				return false, false, err
			}
			if u {
				anyUnknown = true
				continue
			}
			if b {
				return true, false, nil // true dominates
			}
		}
	if anyUnknown {
			return false, true, nil
		}
		return false, false, nil
	}
	return false, false, nil
}
