// Package predicate defines the filter predicate tree and its evaluation.
package predicate

import "fmt"

// Kind discriminates the node types of a predicate tree.
type Kind int

const (
	Compare Kind = iota // column comparison leaf
	And                 // conjunction of Children
	Or                  // disjunction of Children
	Not                 // negation of Children[0]
	Const               // constant boolean leaf
)

// Op is a comparison operator.
type Op string

const (
	Eq       Op = "="
	Ne       Op = "!="
	Lt       Op = "<"
	Gt       Op = ">"
	Le       Op = "<="
	Ge       Op = ">="
	IsNull   Op = "IS NULL"
	IsNotNul Op = "IS NOT NULL"
)

// Node is one node of a predicate tree. Compare uses Column/Op/Value,
// Const uses Bool, And/Or/Not use Children.
type Node struct {
	Kind     Kind
	Column   string
	Op       Op
	Value    any
	Bool     bool
	Children []*Node
}

// Ref locates one column reference inside a predicate tree: Column is the
// referenced column name, Path is its position (e.g. "$/OR[1]/NOT").
type Ref struct {
	Column string
	Path   string
}

// Cmp builds a column comparison leaf.
func Cmp(col string, op Op, val any) *Node {
	return &Node{Kind: Compare, Column: col, Op: op, Value: val}
}

// Boolean builds a constant leaf.
func Boolean(v bool) *Node { return &Node{Kind: Const, Bool: v} }

// AndAll builds an AND node.
func AndAll(kids ...*Node) *Node { return &Node{Kind: And, Children: kids} }

// OrAll builds an OR node.
func OrAll(kids ...*Node) *Node { return &Node{Kind: Or, Children: kids} }

// NotNode builds a NOT node.
func NotNode(kid *Node) *Node { return &Node{Kind: Not, Children: []*Node{kid}} }

// NodeCount returns the total number of nodes in the tree.
func NodeCount(n *Node) int {
	if n == nil {
		return 0
	}
	total := 1
	for _, c := range n.Children {
		total += NodeCount(c)
	}
	return total
}

// Eval evaluates the predicate against row. Every column the predicate
// references must be present in row, or an error is returned.
func Eval(n *Node, row map[string]any) (bool, error) {
	switch n.Kind {
	case Const:
		return n.Bool, nil
	case And:
		for _, c := range n.Children {
			v, err := Eval(c, row)
			if err != nil {
				return false, err
			}
			if !v {
				return false, nil
			}
		}
		return true, nil
	case Or:
		for _, c := range n.Children {
			v, err := Eval(c, row)
			if err != nil {
				return false, err
			}
			if v {
				return true, nil
			}
		}
		return false, nil
	case Not:
		v, err := Eval(n.Children[0], row)
		return !v, err
	case Compare:
		val, ok := row[n.Column]
		if !ok {
			return false, fmt.Errorf("column %q not in row", n.Column)
		}
		return compare(val, n.Op, n.Value)
	}
	return false, fmt.Errorf("unknown node kind %d", n.Kind)
}

func compare(cell any, op Op, arg any) (bool, error) {
	switch op {
	case IsNull:
		return cell == nil, nil
	case IsNotNul:
		return cell != nil, nil
	}
	if cell == nil {
		return false, fmt.Errorf("cannot compare NULL with %s", op)
	}
	ord, err := order(cell, arg)
	if err != nil {
		return false, err
	}
	switch op {
	case Eq:
		return ord == 0, nil
	case Ne:
		return ord != 0, nil
	case Lt:
		return ord < 0, nil
	case Gt:
		return ord > 0, nil
	case Le:
		return ord <= 0, nil
	case Ge:
		return ord >= 0, nil
	}
	return false, fmt.Errorf("unknown op %q", op)
}

// order returns -1/0/1 comparing a and b for ints, floats and strings.
func order(a, b any) (int, error) {
	fa, oka := num(a)
	fb, okb := num(b)
	if oka && okb {
		switch {
		case fa < fb:
			return -1, nil
		case fa > fb:
			return 1, nil
		}
		return 0, nil
	}
	sa, oka := a.(string)
	sb, okb := b.(string)
	if oka && okb {
		switch {
		case sa < sb:
			return -1, nil
		case sa > sb:
			return 1, nil
		}
		return 0, nil
	}
	return 0, fmt.Errorf("incomparable values %v and %v", a, b)
}

func num(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}
