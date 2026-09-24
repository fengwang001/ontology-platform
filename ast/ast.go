// Package ast defines the query predicate tree and canonical printing.
package ast

import (
	"fmt"
	"strconv"
	"strings"
)

// Tri is a Kleene three-valued-logic truth value.
type Tri int

const (
	False Tri = iota
	Unknown
	True
)

// String renders a Tri value.
func (t Tri) String() string {
	switch t {
	case True:
		return "TRUE"
	case False:
		return "FALSE"
	default:
		return "UNKNOWN"
	}
}

// CmpOp is a comparison operator.
type CmpOp int

const (
	Eq CmpOp = iota
	Ne
	Lt
	Le
	Gt
	Ge
)

func (op CmpOp) String() string {
	return [...]string{"=", "<>", "<", "<=", ">", ">="}[op]
}

// Neg returns the negated comparison (NOT (a op b) == a Neg(op) b).
func (op CmpOp) Neg() CmpOp {
	return [...]CmpOp{Ne, Eq, Ge, Gt, Le, Lt}[op]
}

// Expr is a predicate expression node.
type Expr interface {
	String() string
}

// Const is a three-valued constant.
type Const struct{ V Tri }

// Col is a column reference: table T, column index C.
type Col struct {
	T string
	C int
}

// Int is an integer constant operand.
type Int struct{ N int }

// Cmp compares two operands (Expr nodes that are Col, Int, or Const).
type Cmp struct {
	Op   CmpOp
	L, R Expr
}

// Logic is an AND/OR node with one or more children.
type Logic struct {
	Op    string // "AND" or "OR"
	Child []Expr
}

// Not is logical negation.
type Not struct{ E Expr }

func (c Const) String() string { return c.V.String() }
func (c Col) String() string   { return fmt.Sprintf("%s.c%d", c.T, c.C) }
func (n Int) String() string   { return strconv.Itoa(n.N) }
func (x Cmp) String() string   { return Print(x) }
func (x Not) String() string   { return Print(x) }
func (x Logic) String() string { return Print(x) }

// Print returns the canonical string of an expression; Col renders as t.cN.
func Print(e Expr) string {
	switch x := e.(type) {
	case nil:
		return "<nil>"
	case Col:
		return x.String()
	case Const:
		return x.String()
	case Int:
		return x.String()
	case Cmp:
		return "(" + Print(x.L) + " " + x.Op.String() + " " + Print(x.R) + ")"
	case Not:
		return "(NOT " + Print(x.E) + ")"
	case Logic:
		parts := make([]string, len(x.Child))
		for i, c := range x.Child {
			parts[i] = Print(c)
		}
		return "(" + strings.Join(parts, " "+x.Op+" ") + ")"
	default:
		panic("ast: unknown node")
	}
}

// Constructors.

// K returns a constant expression.
func K(v Tri) Expr { return Const{V: v} }

// C builds a comparison.
func C(op CmpOp, l, r Expr) Expr { return Cmp{Op: op, L: l, R: r} }

// And builds an AND node.
func And(es ...Expr) Expr { return Logic{Op: "AND", Child: es} }

// Or builds an OR node.
func Or(es ...Expr) Expr { return Logic{Op: "OR", Child: es} }

// N builds a NOT node.
func N(e Expr) Expr { return Not{E: e} }

// Count returns the number of nodes in the tree (iterative, no recursion).
func Count(e Expr) int {
	if e == nil {
		return 0
	}
	n := 0
	stack := []Expr{e}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n++
		switch z := x.(type) {
		case Cmp:
			stack = append(stack, z.L, z.R)
		case Not:
			stack = append(stack, z.E)
		case Logic:
			stack = append(stack, z.Child...)
		}
	}
	return n
}

// Tables returns the set of tables referenced by the expression.
func Tables(e Expr) map[string]bool {
	out := map[string]bool{}
	if e == nil {
		return out
	}
	stack := []Expr{e}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch z := x.(type) {
		case Col:
			out[z.T] = true
		case Cmp:
			stack = append(stack, z.L, z.R)
		case Not:
			stack = append(stack, z.E)
		case Logic:
			stack = append(stack, z.Child...)
		}
	}
	return out
}
