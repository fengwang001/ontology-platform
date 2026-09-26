// Package ast defines the expression AST folded by the folders in this module.
//
// Nodes: IntLit, BoolLit, Var, Neg, Not, BinOp(op,l,r), If(cond,then,else).
// The package depends on nothing else in the module.
package ast

import "strconv"

// Kind identifies the concrete node type held by an Expr.
type Kind int

const (
	KInt  Kind = iota // IntLit: value in I
	KBool             // BoolLit: value in B
	KVar              // variable: name in Name
	KNeg              // unary -: child in Sub
	KNot              // unary !: child in Sub
	KBin              // BinOp: op in Op, operands in L, R
	KIf               // if-then-else: C, T, E
)

// Expr is a single AST node. Unused fields are ignored based on Kind.
type Expr struct {
	Kind    Kind
	Op      string
	I       int64
	B       bool
	Name    string
	Sub     *Expr
	L, R    *Expr
	C, T, E *Expr
}

// Constructors, one per node kind.

func Int(v int64) *Expr               { return &Expr{Kind: KInt, I: v} }
func Bool(v bool) *Expr               { return &Expr{Kind: KBool, B: v} }
func Var(name string) *Expr           { return &Expr{Kind: KVar, Name: name} }
func Neg(x *Expr) *Expr               { return &Expr{Kind: KNeg, Sub: x} }
func Not(x *Expr) *Expr               { return &Expr{Kind: KNot, Sub: x} }
func Bin(op string, l, r *Expr) *Expr { return &Expr{Kind: KBin, Op: op, L: l, R: r} }
func If(c, t, e *Expr) *Expr          { return &Expr{Kind: KIf, C: c, T: t, E: e} }

// DeepCopy returns a structurally independent copy of n (nil in, nil out).
func DeepCopy(n *Expr) *Expr {
	if n == nil {
		return nil
	}
	m := *n
	switch n.Kind {
	case KNeg, KNot:
		m.Sub = DeepCopy(n.Sub)
	case KBin:
		m.L, m.R = DeepCopy(n.L), DeepCopy(n.R)
	case KIf:
		m.C, m.T, m.E = DeepCopy(n.C), DeepCopy(n.T), DeepCopy(n.E)
	}
	return &m
}

// Equal reports structural equality of two ASTs (two nils are equal).
func Equal(a, b *Expr) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind != b.Kind || a.Op != b.Op || a.I != b.I || a.B != b.B || a.Name != b.Name {
		return false
	}
	switch a.Kind {
	case KInt, KBool, KVar:
		return true
	case KNeg, KNot:
		return Equal(a.Sub, b.Sub)
	case KBin:
		return Equal(a.L, b.L) && Equal(a.R, b.R)
	case KIf:
		return Equal(a.C, b.C) && Equal(a.T, b.T) && Equal(a.E, b.E)
	}
	return false
}

// String renders the node in fully parenthesized form for diagnostics.
func (n *Expr) String() string {
	if n == nil {
		return "<nil>"
	}
	switch n.Kind {
	case KInt:
		return strconv.FormatInt(n.I, 10)
	case KBool:
		return strconv.FormatBool(n.B)
	case KVar:
		return n.Name
	case KNeg:
		return "(-" + n.Sub.String() + ")"
	case KNot:
		return "(!" + n.Sub.String() + ")"
	case KBin:
		return "(" + n.L.String() + " " + n.Op + " " + n.R.String() + ")"
	case KIf:
		return "(if " + n.C.String() + " " + n.T.String() + " " + n.E.String() + ")"
	}
	return "<?>"
}
