// Package ast defines the expression AST plus a reference evaluator used to
// pin the TAC generator's semantics. It depends on no other package.
package ast

import "errors"

// Kind discriminates the node type of an Expr.
type Kind int

const (
	KInt   Kind = iota // int literal, Int holds the value
	KBool              // bool literal, Bool holds the value
	KVar               // variable, Name holds the identifier
	KBin               // binary arith/compare, Op in + - * / < > <= >= == !=
	KLogic             // short-circuit logic, Op in && ||
	KNot               // logical not, L is the operand
	KNeg               // arithmetic negation, L is the operand
	KIf                // if-then-else, C/T/E are cond/then/else
)

// Expr is one expression node. Fields not implied by Kind are zero.
type Expr struct {
	Kind Kind
	Op   string
	Int  int64
	Bool bool
	Name string
	L    *Expr
	R    *Expr
	C    *Expr
	T    *Expr
	E    *Expr
}

func Int(v int64) *Expr     { return &Expr{Kind: KInt, Int: v} }
func Bool(v bool) *Expr     { return &Expr{Kind: KBool, Bool: v} }
func Var(name string) *Expr { return &Expr{Kind: KVar, Name: name} }

// Bin builds an arithmetic or comparison node; l evaluates before r.
func Bin(op string, l, r *Expr) *Expr { return &Expr{Kind: KBin, Op: op, L: l, R: r} }

// Logic builds a short-circuit && or || node.
func Logic(op string, l, r *Expr) *Expr { return &Expr{Kind: KLogic, Op: op, L: l, R: r} }

func Not(x *Expr) *Expr { return &Expr{Kind: KNot, L: x} }
func Neg(x *Expr) *Expr { return &Expr{Kind: KNeg, L: x} }

// If builds an if-then-else node; then and else must both be non-nil.
func If(c, t, e *Expr) *Expr { return &Expr{Kind: KIf, C: c, T: t, E: e} }

var (
	errDivZero = errors.New("ast: division by zero")
	errBadNode = errors.New("ast: bad node")
)

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func arith(op string, a, b int64) (int64, error) {
	if op == "/" && b == 0 {
		return 0, errDivZero
	}
	var v int64
	switch op {
	case "+":
		v = a + b
	case "-":
		v = a - b
	case "*":
		v = a * b
	case "/":
		v = a / b
	case "<":
		v = b2i(a < b)
	case ">":
		v = b2i(a > b)
	case "<=":
		v = b2i(a <= b)
	case ">=":
		v = b2i(a >= b)
	case "==":
		v = b2i(a == b)
	case "!=":
		v = b2i(a != b)
	}
	return v, nil
}

// Eval is the reference evaluator: recursive, short-circuit, left-to-right,
// left-associative as given by the tree shape. Booleans are 0/1.
func Eval(n *Expr, env map[string]int64) (int64, error) {
	if n == nil {
		return 0, errBadNode
	}
	switch n.Kind {
	case KInt:
		return n.Int, nil
	case KBool:
		return b2i(n.Bool), nil
	case KVar:
		return env[n.Name], nil
	case KBin:
		l, err := Eval(n.L, env)
		if err != nil {
			return 0, err
		}
		r, err := Eval(n.R, env)
		if err != nil {
			return 0, err
		}
		return arith(n.Op, l, r)
	case KLogic:
		l, err := Eval(n.L, env)
		if err != nil {
			return 0, err
		}
		if n.Op == "&&" && l == 0 || n.Op == "||" && l != 0 {
			return l, nil
		}
		return Eval(n.R, env)
	case KNot:
		v, err := Eval(n.L, env)
		return b2i(v == 0), err
	case KNeg:
		v, err := Eval(n.L, env)
		return -v, err
	case KIf:
		c, err := Eval(n.C, env)
		if err != nil {
			return 0, err
		}
		if c != 0 {
			return Eval(n.T, env)
		}
		return Eval(n.E, env)
	}
	return 0, errBadNode
}
