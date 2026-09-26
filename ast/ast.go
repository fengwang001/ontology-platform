// Package ast defines the expression AST, a recursive deep copy and a
// structural equality check. It depends on nothing.
package ast

// Kind discriminates the node type stored in an Expr.
type Kind int

const (
	KInt  Kind = iota // int literal, value in Int
	KBool             // bool literal, value in Bool
	KVar              // variable, name in Name; never folded
	KNeg              // unary -, operand in L
	KNot              // unary !, operand in L
	KBin              // binary op in Op, operands in L and R
	KIf               // if-then-else, C/T/E
)

// Expr is a single AST node. Fields not relevant to a Kind are zero.
type Expr struct {
	Kind Kind
	Int  int64  // KInt
	Bool bool   // KBool
	Name string // KVar
	Op   string // KBin: + - * / < > <= >= == != && ||
	L, R *Expr  // KNeg/KNot: L; KBin: L,R
	C    *Expr  // KIf: condition
	T    *Expr  // KIf: then branch
	E    *Expr  // KIf: else branch
}

// Int builds an int literal.
func Int(v int64) *Expr { return &Expr{Kind: KInt, Int: v} }

// Bool builds a bool literal.
func Bool(v bool) *Expr { return &Expr{Kind: KBool, Bool: v} }

// Var builds a variable reference.
func Var(name string) *Expr { return &Expr{Kind: KVar, Name: name} }

// Neg builds unary minus.
func Neg(e *Expr) *Expr { return &Expr{Kind: KNeg, L: e} }

// Not builds logical negation.
func Not(e *Expr) *Expr { return &Expr{Kind: KNot, L: e} }

// Bin builds a binary operation.
func Bin(op string, l, r *Expr) *Expr { return &Expr{Kind: KBin, Op: op, L: l, R: r} }

// If builds an if-then-else.
func If(c, t, e *Expr) *Expr { return &Expr{Kind: KIf, C: c, T: t, E: e} }

// Copy returns a recursive deep copy of n (nil-safe).
func Copy(n *Expr) *Expr {
	if n == nil {
		return nil
	}
	c := *n
	c.L, c.R = Copy(n.L), Copy(n.R)
	c.C, c.T, c.E = Copy(n.C), Copy(n.T), Copy(n.E)
	return &c
}

// Equal reports whether a and b are structurally identical, node by node.
func Equal(a, b *Expr) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Kind == b.Kind && a.Int == b.Int && a.Bool == b.Bool &&
		a.Name == b.Name && a.Op == b.Op &&
		Equal(a.L, b.L) && Equal(a.R, b.R) &&
		Equal(a.C, b.C) && Equal(a.T, b.T) && Equal(a.E, b.E)
}
