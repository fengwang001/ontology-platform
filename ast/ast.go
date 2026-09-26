// Package ast defines the expression AST and structural equality.
package ast

// Kind is the node type tag (matches the wire tag 0..6).
type Kind byte

const (
	IntLit Kind = iota
	BoolLit
	Var
	Neg
	Not
	BinOp
	If
)

// Op is a binary operator; its value is the 1-byte wire code.
type Op byte

const (
	OpAdd Op = iota // +
	OpSub           // -
	OpMul           // *
	OpDiv           // /
	OpLt            // <
	OpGt            // >
	OpLe            // <=
	OpGe            // >=
	OpEq            // ==
	OpNe            // !=
	OpAnd           // &&
	OpOr            // ||
)

// Expr is one AST node. Only the fields relevant to Kind are set.
type Expr struct {
	Kind  Kind
	I     int64  // IntLit
	B     bool   // BoolLit
	Name  string // Var
	Op    Op     // BinOp
	Child *Expr  // Neg, Not
	L, R  *Expr  // BinOp
	Cond  *Expr  // If
	Then  *Expr  // If
	Else  *Expr  // If
}

func Int(v int64) *Expr   { return &Expr{Kind: IntLit, I: v} }
func Bool(v bool) *Expr   { return &Expr{Kind: BoolLit, B: v} }
func Name(s string) *Expr { return &Expr{Kind: Var, Name: s} }
func NegOf(c *Expr) *Expr { return &Expr{Kind: Neg, Child: c} }
func NotOf(c *Expr) *Expr { return &Expr{Kind: Not, Child: c} }

func Bin(op Op, l, r *Expr) *Expr { return &Expr{Kind: BinOp, Op: op, L: l, R: r} }

func IfElse(c, t, e *Expr) *Expr { return &Expr{Kind: If, Cond: c, Then: t, Else: e} }

// Equal reports structural equality: same kind, same scalar fields
// (Var names compared bytewise), and recursively equal children.
func Equal(a, b *Expr) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case IntLit:
		return a.I == b.I
	case BoolLit:
		return a.B == b.B
	case Var:
		return a.Name == b.Name // Go string == is bytewise
	case Neg, Not:
		return Equal(a.Child, b.Child)
	case BinOp:
		return a.Op == b.Op && Equal(a.L, b.L) && Equal(a.R, b.R)
	case If:
		return Equal(a.Cond, b.Cond) && Equal(a.Then, b.Then) && Equal(a.Else, b.Else)
	}
	return false
}
