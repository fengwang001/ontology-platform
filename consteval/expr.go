package consteval

import (
	"fmt"
	"math/big"
	"strings"
)

// Op identifies the operation of an expression node.
type Op int

const (
	OpLit     Op = iota // literal, 0 operands
	OpName              // named-constant reference, 0 operands
	OpConvert           // conversion to Node.Type, 1 operand
	OpNeg               // -x
	OpBitNot            // ^x
	OpNot               // !x
	OpAdd               // x + y
	OpSub               // x - y
	OpMul               // x * y
	OpDiv               // x / y
	OpMod               // x % y
	OpBitAnd            // x & y
	OpBitOr             // x | y
	OpBitXor            // x ^ y
	OpShl               // x << y
	OpShr               // x >> y
	OpEq                // x == y
	OpNe                // x != y
	OpLt                // x < y
	OpLe                // x <= y
	OpGt                // x > y
	OpGe                // x >= y
	OpLogAnd            // x && y (both sides always evaluated)
	OpLogOr             // x || y (both sides always evaluated)
	opCount
)

// opArity gives the required operand count per operator; -1 means leaf.
var opArity = [opCount]int{
	OpLit: 0, OpName: 0, OpConvert: 1,
	OpNeg: 1, OpBitNot: 1, OpNot: 1,
	OpAdd: 2, OpSub: 2, OpMul: 2, OpDiv: 2, OpMod: 2,
	OpBitAnd: 2, OpBitOr: 2, OpBitXor: 2, OpShl: 2, OpShr: 2,
	OpEq: 2, OpNe: 2, OpLt: 2, OpLe: 2, OpGt: 2, OpGe: 2,
	OpLogAnd: 2, OpLogOr: 2,
}

var opSymbol = map[Op]string{
	OpNeg: "-", OpBitNot: "^", OpNot: "!",
	OpAdd: "+", OpSub: "-", OpMul: "*", OpDiv: "/", OpMod: "%",
	OpBitAnd: "&", OpBitOr: "|", OpBitXor: "^", OpShl: "<<", OpShr: ">>",
	OpEq: "==", OpNe: "!=", OpLt: "<", OpLe: "<=", OpGt: ">", OpGe: ">=",
	OpLogAnd: "&&", OpLogOr: "||",
}

// Lit is the payload of an OpLit node. Exactly the field matching Kind
// must be set. Literal nodes built via the constructors in this file are
// always well-formed.
type Lit struct {
	Kind Kind
	Int  *big.Int
	Rat  *big.Rat
	Bool bool
	Str  string
}

// Node is one node of a constant-expression tree built by the caller.
// Args holds operands; Lit, Name and Type are payloads for OpLit,
// OpName and OpConvert respectively.
type Node struct {
	Op   Op
	Args []*Node
	Lit  Lit
	Name string
	Type string
}

// IntLit builds an untyped integer literal from its decimal
// representation. It panics on malformed input (a caller bug).
func IntLit(decimal string) *Node {
	v, ok := new(big.Int).SetString(decimal, 10)
	if !ok {
		panic("consteval: malformed integer literal " + decimal)
	}
	return BigIntLit(v)
}

// BigIntLit builds an untyped integer literal from a big.Int.
func BigIntLit(v *big.Int) *Node {
	return &Node{Op: OpLit, Lit: Lit{Kind: KindInt, Int: new(big.Int).Set(v)}}
}

// RatLit builds an untyped rational literal. The string is parsed by
// big.Rat.SetString and accepts fractions ("3/4"), decimals ("1.5") and
// exponents ("1e-3"). It panics on malformed input.
func RatLit(s string) *Node {
	v, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("consteval: malformed rational literal " + s)
	}
	return &Node{Op: OpLit, Lit: Lit{Kind: KindRational, Rat: v}}
}

// BoolLit builds an untyped bool literal.
func BoolLit(v bool) *Node {
	return &Node{Op: OpLit, Lit: Lit{Kind: KindBool, Bool: v}}
}

// StrLit builds an untyped string literal.
func StrLit(v string) *Node {
	return &Node{Op: OpLit, Lit: Lit{Kind: KindString, Str: v}}
}

// Name builds a reference to a registered named constant.
func Name(n string) *Node {
	return &Node{Op: OpName, Name: n}
}

// Convert builds an explicit conversion of expr to the named type.
func Convert(expr *Node, typeName string) *Node {
	return &Node{Op: OpConvert, Args: []*Node{expr}, Type: typeName}
}

// Un builds a unary expression.
func Un(op Op, a *Node) *Node {
	return &Node{Op: op, Args: []*Node{a}}
}

// Bin builds a binary expression.
func Bin(op Op, a, b *Node) *Node {
	return &Node{Op: op, Args: []*Node{a, b}}
}

// String renders the tree in a compact parenthesized form, used in test
// logs.
func (n *Node) String() string {
	if n == nil {
		return "<nil>"
	}
	switch n.Op {
	case OpLit:
		switch n.Lit.Kind {
		case KindInt:
			return n.Lit.Int.String()
		case KindRational:
			return n.Lit.Rat.RatString()
		case KindBool:
			return fmt.Sprintf("%t", n.Lit.Bool)
		case KindString:
			return fmt.Sprintf("%q", n.Lit.Str)
		}
		return "<bad lit>"
	case OpName:
		return fmt.Sprintf("name(%q)", n.Name)
	case OpConvert:
		return fmt.Sprintf("(%s)(%s)", n.Type, n.Args[0])
	}
	if len(n.Args) == 1 {
		return fmt.Sprintf("(%s%s)", opSymbol[n.Op], n.Args[0])
	}
	if len(n.Args) == 2 {
		return fmt.Sprintf("(%s %s %s)", n.Args[0], opSymbol[n.Op], n.Args[1])
	}
	return fmt.Sprintf("<bad node op=%d argc=%d>", int(n.Op), len(n.Args))
}

// validateStructure checks tree shape: operator validity, operand
// counts, literal payload consistency, non-empty reference names and
// known conversion type names. Any violation is ErrInvalidArgument and
// outranks every evaluation error.
func validateStructure(n *Node) *Error {
	if n == nil {
		return errf(ErrInvalidArgument, "nil expression node")
	}
	if n.Op < 0 || n.Op >= opCount {
		return errf(ErrInvalidArgument, "unknown operator %d", int(n.Op))
	}
	if len(n.Args) != opArity[n.Op] {
		return errf(ErrInvalidArgument, "operator %v expects %d operand(s), got %d",
			n.Op, opArity[n.Op], len(n.Args))
	}
	switch n.Op {
	case OpLit:
		switch n.Lit.Kind {
		case KindInt:
			if n.Lit.Int == nil {
				return errf(ErrInvalidArgument, "integer literal without value")
			}
		case KindRational:
			if n.Lit.Rat == nil {
				return errf(ErrInvalidArgument, "rational literal without value")
			}
		case KindBool, KindString:
		default:
			return errf(ErrInvalidArgument, "literal with invalid kind %d", int(n.Lit.Kind))
		}
	case OpName:
		if n.Name == "" {
			return errf(ErrInvalidArgument, "reference to empty name")
		}
	case OpConvert:
		if _, ok := ParseType(n.Type); !ok {
			return errf(ErrInvalidArgument, "unknown type name %q", n.Type)
		}
	}
	for _, a := range n.Args {
		if err := validateStructure(a); err != nil {
			return err
		}
	}
	return nil
}

// collectNames gathers every referenced name in the tree.
func collectNames(n *Node, into map[string]struct{}) {
	if n == nil {
		return
	}
	if n.Op == OpName {
		into[n.Name] = struct{}{}
	}
	for _, a := range n.Args {
		collectNames(a, into)
	}
}

// describe renders a node list for error messages.
func describe(nodes ...*Node) string {
	parts := make([]string, len(nodes))
	for i, n := range nodes {
		parts[i] = n.String()
	}
	return strings.Join(parts, ", ")
}
