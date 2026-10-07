package consteval

import (
	"fmt"
	"math/big"
)

// Op identifies a unary or binary operator.
type Op int

const (
	OpAdd    Op = iota // +
	OpSub              // -
	OpMul              // *
	OpQuo              // /
	OpRem              // %
	OpAnd              // &
	OpOr               // |
	OpXor              // ^
	OpShl              // <<
	OpShr              // >>
	OpEql              // ==
	OpNeq              // !=
	OpLss              // <
	OpLeq              // <=
	OpGtr              // >
	OpGeq              // >=
	OpLAnd             // &&
	OpLOr              // ||
	OpNeg              // unary -
	OpNot              // !
	OpBitNot           // unary ^
)

var opNames = map[Op]string{
	OpAdd: "+", OpSub: "-", OpMul: "*", OpQuo: "/", OpRem: "%",
	OpAnd: "&", OpOr: "|", OpXor: "^", OpShl: "<<", OpShr: ">>",
	OpEql: "==", OpNeq: "!=", OpLss: "<", OpLeq: "<=", OpGtr: ">", OpGeq: ">=",
	OpLAnd: "&&", OpLOr: "||", OpNeg: "neg", OpNot: "!", OpBitNot: "bitnot",
}

func (o Op) String() string {
	if s, ok := opNames[o]; ok {
		return s
	}
	return fmt.Sprintf("op(%d)", int(o))
}

func (o Op) valid() bool { return o >= OpAdd && o <= OpBitNot }

func (o Op) isUnary() bool { return o >= OpNeg && o <= OpBitNot }

// NodeKind discriminates expression tree nodes.
type NodeKind int

const (
	LitInt NodeKind = iota
	LitRat
	LitBool
	LitStr
	Ref
	Unary
	Binary
	Convert // annotate an untyped subexpression with a concrete type
)

// Expr is a node of a structured constant expression tree built by the
// caller. Use the constructor helpers (IntLit, Add, ...) to build trees.
type Expr struct {
	Node NodeKind
	Op   Op      // Unary / Binary
	Args []*Expr // operator operands
	// Literal payloads.
	Int  string // LitInt: decimal integer literal
	Rat  string // LitRat: "num/den" or decimal rational literal
	Bool bool   // LitBool
	Str  string // LitStr
	// Reference / conversion payloads.
	Name string // Ref: referenced constant name
	Type Type   // Convert: target type
}

// Constructor helpers.

func IntLit(decimal string) *Expr { return &Expr{Node: LitInt, Int: decimal} }
func RatLit(rat string) *Expr     { return &Expr{Node: LitRat, Rat: rat} }
func BoolLit(b bool) *Expr        { return &Expr{Node: LitBool, Bool: b} }
func StrLit(s string) *Expr       { return &Expr{Node: LitStr, Str: s} }
func NameRef(name string) *Expr   { return &Expr{Node: Ref, Name: name} }
func To(t Type, e *Expr) *Expr    { return &Expr{Node: Convert, Type: t, Args: []*Expr{e}} }
func Neg(e *Expr) *Expr           { return &Expr{Node: Unary, Op: OpNeg, Args: []*Expr{e}} }
func Not(e *Expr) *Expr           { return &Expr{Node: Unary, Op: OpNot, Args: []*Expr{e}} }
func BitNot(e *Expr) *Expr        { return &Expr{Node: Unary, Op: OpBitNot, Args: []*Expr{e}} }
func Bin(op Op, l, r *Expr) *Expr { return &Expr{Node: Binary, Op: op, Args: []*Expr{l, r}} }
func Add(l, r *Expr) *Expr        { return Bin(OpAdd, l, r) }
func Sub(l, r *Expr) *Expr        { return Bin(OpSub, l, r) }
func Mul(l, r *Expr) *Expr        { return Bin(OpMul, l, r) }
func Quo(l, r *Expr) *Expr        { return Bin(OpQuo, l, r) }
func Rem(l, r *Expr) *Expr        { return Bin(OpRem, l, r) }
func And(l, r *Expr) *Expr        { return Bin(OpAnd, l, r) }
func Or(l, r *Expr) *Expr         { return Bin(OpOr, l, r) }
func Xor(l, r *Expr) *Expr        { return Bin(OpXor, l, r) }
func Shl(l, r *Expr) *Expr        { return Bin(OpShl, l, r) }
func Shr(l, r *Expr) *Expr        { return Bin(OpShr, l, r) }
func Eql(l, r *Expr) *Expr        { return Bin(OpEql, l, r) }
func Neq(l, r *Expr) *Expr        { return Bin(OpNeq, l, r) }
func Lss(l, r *Expr) *Expr        { return Bin(OpLss, l, r) }
func Leq(l, r *Expr) *Expr        { return Bin(OpLeq, l, r) }
func Gtr(l, r *Expr) *Expr        { return Bin(OpGtr, l, r) }
func Geq(l, r *Expr) *Expr        { return Bin(OpGeq, l, r) }
func LAnd(l, r *Expr) *Expr       { return Bin(OpLAnd, l, r) }
func LOr(l, r *Expr) *Expr        { return Bin(OpLOr, l, r) }

// validate checks the tree structure: known operators, correct operand
// counts, non-empty reference names, known conversion targets. Structural
// failures are ErrInvalidArgument and outrank every evaluation error.
func (e *Expr) validate() error {
	if e == nil {
		return errf(ErrInvalidArgument, "nil expression node")
	}
	switch e.Node {
	case LitInt:
		if _, ok := new(big.Int).SetString(e.Int, 10); !ok {
			return errf(ErrInvalidArgument, "malformed integer literal %q", e.Int)
		}
	case LitRat:
		if _, ok := new(big.Rat).SetString(e.Rat); !ok {
			return errf(ErrInvalidArgument, "malformed rational literal %q", e.Rat)
		}
	case LitBool, LitStr:
	case Ref:
		if e.Name == "" {
			return errf(ErrInvalidArgument, "empty constant name reference")
		}
	case Unary:
		if !e.Op.valid() || !e.Op.isUnary() {
			return errf(ErrInvalidArgument, "operator %s is not a valid unary operator", e.Op)
		}
		if len(e.Args) != 1 {
			return errf(ErrInvalidArgument, "unary %s takes 1 operand, got %d", e.Op, len(e.Args))
		}
	case Binary:
		if !e.Op.valid() || e.Op.isUnary() {
			return errf(ErrInvalidArgument, "operator %s is not a valid binary operator", e.Op)
		}
		if len(e.Args) != 2 {
			return errf(ErrInvalidArgument, "binary %s takes 2 operands, got %d", e.Op, len(e.Args))
		}
	case Convert:
		if e.Type == NoType || e.Type < Int8 || e.Type > String {
			return errf(ErrInvalidArgument, "invalid conversion target type %d", int(e.Type))
		}
		if len(e.Args) != 1 {
			return errf(ErrInvalidArgument, "conversion takes 1 operand, got %d", len(e.Args))
		}
	default:
		return errf(ErrInvalidArgument, "unknown node kind %d", int(e.Node))
	}
	for _, a := range e.Args {
		if err := a.validate(); err != nil {
			return err
		}
	}
	return nil
}

// refNames collects the names referenced by the tree (left to right).
func (e *Expr) refNames(out *[]string) {
	if e == nil {
		return
	}
	if e.Node == Ref {
		*out = append(*out, e.Name)
	}
	for _, a := range e.Args {
		a.refNames(out)
	}
}
