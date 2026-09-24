// Package predicate defines a filter predicate tree and constant folding.
package predicate

// Kind enumerates predicate node kinds.
type Kind int

const (
	Const Kind = iota // boolean literal
	Cmp               // column comparison
	And
	Or
	Not
)

// Op enumerates comparison operators.
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

// Node is one predicate tree node. Fields are used per Kind:
// Const uses Bool; Cmp uses Col/Op/Val; And/Or use Children; Not uses Child.
type Node struct {
	Kind     Kind
	Bool     bool
	Col      string
	Op       Op
	Val      any
	Children []*Node
	Child    *Node
}

// True returns a constant-true node.
func True() *Node { return &Node{Kind: Const, Bool: true} }

// False returns a constant-false node.
func False() *Node { return &Node{Kind: Const} }

// Compare returns a column comparison node.
func Compare(col string, op Op, val any) *Node {
	return &Node{Kind: Cmp, Col: col, Op: op, Val: val}
}

// AndOf returns an AND node over kids.
func AndOf(kids ...*Node) *Node { return &Node{Kind: And, Children: kids} }

// OrOf returns an OR node over kids.
func OrOf(kids ...*Node) *Node { return &Node{Kind: Or, Children: kids} }

// NotOf returns a NOT node over child.
func NotOf(child *Node) *Node { return &Node{Kind: Not, Child: child} }

// NodeCount returns the total number of nodes in the tree.
func NodeCount(n *Node) int {
	if n == nil {
		return 0
	}
	c := 1
	for _, k := range n.Children {
		c += NodeCount(k)
	}
	if n.Child != nil {
		c += NodeCount(n.Child)
	}
	return c
}

// Fold constant-folds the tree, applying absorption laws:
// X OR true -> true, X AND false -> false (X is discarded, never evaluated),
// X OR false -> X, X AND true -> X. Returns a new tree; n is not mutated.
func Fold(n *Node) *Node {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case And:
		kids := make([]*Node, 0, len(n.Children))
		for _, c := range n.Children {
			c = Fold(c)
			if c != nil && c.Kind == Const {
				if !c.Bool {
					return False()
				}
				continue
			}
			kids = append(kids, c)
		}
		switch len(kids) {
		case 0:
			return True()
		case 1:
			return kids[0]
		}
		return &Node{Kind: And, Children: kids}
	case Or:
		kids := make([]*Node, 0, len(n.Children))
		for _, c := range n.Children {
			c = Fold(c)
			if c != nil && c.Kind == Const {
				if c.Bool {
					return True()
				}
				continue
			}
			kids = append(kids, c)
		}
		switch len(kids) {
		case 0:
			return False()
		case 1:
			return kids[0]
		}
		return &Node{Kind: Or, Children: kids}
	case Not:
		c := Fold(n.Child)
		if c != nil && c.Kind == Const {
			if c.Bool {
				return False()
			}
			return True()
		}
		return &Node{Kind: Not, Child: c}
	default:
		return n
	}
}
