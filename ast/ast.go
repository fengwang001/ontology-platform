// Package ast defines the expression AST. It depends on nothing.
package ast

// Kind enumerates AST node kinds.
type Kind int

const (
	IntLit  Kind = iota // integer literal, Val
	BoolLit             // boolean literal, BVal
	Var                 // variable reference, Name
	Unary               // unary op ("-" or "!"), Op + Left
	Binary              // binary op, Op + Left + Right
	If                  // if-then-else, Cond + Left(then) + Right(else)
)

// Expr is one AST node. Left/Right double as then/else branches for If.
type Expr struct {
	Kind        Kind
	Op          string
	Val         int64
	BVal        bool
	Name        string
	Left, Right *Expr
	Cond        *Expr
}

func Int(v int64) *Expr   { return &Expr{Kind: IntLit, Val: v} }
func Bool(b bool) *Expr   { return &Expr{Kind: BoolLit, BVal: b} }
func V(name string) *Expr { return &Expr{Kind: Var, Name: name} }
func Neg(e *Expr) *Expr   { return &Expr{Kind: Unary, Op: "-", Left: e} }
func Not(e *Expr) *Expr   { return &Expr{Kind: Unary, Op: "!", Left: e} }
func Bin(op string, l, r *Expr) *Expr {
	return &Expr{Kind: Binary, Op: op, Left: l, Right: r}
}
func IfElse(c, t, e *Expr) *Expr {
	return &Expr{Kind: If, Cond: c, Left: t, Right: e}
}
