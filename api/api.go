// Package api is the public entry point: FoldExpr and SelfCheck.
package api

import (
	"errors"
	"fmt"

	"ontology/ast"
	"ontology/fold"
)

// Determinable, mutually distinct sentinel errors.
var (
	ErrNilNode      = errors.New("api: nil node")
	ErrUnknownOp    = errors.New("api: unknown operator")
	ErrTypeMismatch = errors.New("api: type mismatch")
)

// FoldExpr folds n into a new AST without modifying it. Safe for concurrent use.
func FoldExpr(n *ast.Expr) *ast.Expr { return fold.Fold(n) }

var ops = map[string]bool{
	"+": true, "-": true, "*": true, "/": true, "<": true, ">": true,
	"<=": true, ">=": true, "==": true, "!=": true, "&&": true, "||": true,
}

const (
	tBad = iota
	tAny
	tInt
	tBool
)

func num(t int) bool     { return t == tInt || t == tAny }
func boolean(t int) bool { return t == tBool || t == tAny }

// typeOf infers the expression type; tBad marks a type-inconsistent node.
func typeOf(n *ast.Expr) int {
	switch n.Kind {
	case ast.KInt:
		return tInt
	case ast.KBool:
		return tBool
	case ast.KVar:
		return tAny
	case ast.KNeg:
		if num(typeOf(n.L)) {
			return tInt
		}
	case ast.KNot:
		if boolean(typeOf(n.L)) {
			return tBool
		}
	case ast.KBin:
		lt, rt := typeOf(n.L), typeOf(n.R)
		arith := n.Op == "+" || n.Op == "-" || n.Op == "*" || n.Op == "/"
		cmp := n.Op == "<" || n.Op == ">" || n.Op == "<=" || n.Op == ">="
		logic := n.Op == "&&" || n.Op == "||"
		eq := n.Op == "==" || n.Op == "!="
		switch {
		case arith && num(lt) && num(rt):
			return tInt
		case cmp && num(lt) && num(rt):
			return tBool
		case logic && boolean(lt) && boolean(rt):
			return tBool
		case eq && (lt == rt || lt == tAny || rt == tAny):
			return tBool
		}
	case ast.KIf:
		ct, tt, et := typeOf(n.C), typeOf(n.T), typeOf(n.E)
		if boolean(ct) && (tt == et || tt == tAny || et == tAny) {
			if tt == tAny {
				return et
			}
			return tt
		}
	}
	return tBad
}

// validate reports a determinable error for illegal ASTs; it is pure.
func validate(n *ast.Expr) error {
	if n == nil {
		return ErrNilNode
	}
	var kids []*ast.Expr
	switch n.Kind {
	case ast.KInt, ast.KBool, ast.KVar:
	case ast.KNeg, ast.KNot:
		kids = []*ast.Expr{n.L}
	case ast.KBin:
		if !ops[n.Op] {
			return ErrUnknownOp
		}
		kids = []*ast.Expr{n.L, n.R}
	case ast.KIf:
		kids = []*ast.Expr{n.C, n.T, n.E}
	}
	for _, k := range kids {
		if err := validate(k); err != nil {
			return err
		}
	}
	if typeOf(n) == tBad {
		return ErrTypeMismatch
	}
	return nil
}

// SelfCheck verifies the four invariants on a built-in suite of ASTs.
func SelfCheck() error {
	bad := []struct {
		n    *ast.Expr
		want error
	}{ // invariant 4: three distinct determinable errors
		{ast.Not(nil), ErrNilNode},
		{ast.Bin("%", ast.Int(1), ast.Int(2)), ErrUnknownOp},
		{ast.Bin("+", ast.Int(1), ast.Bool(true)), ErrTypeMismatch},
	}
	for _, c := range bad {
		if err := validate(c.n); !errors.Is(err, c.want) {
			return fmt.Errorf("selfcheck: want %v, got %v", c.want, err)
		}
	}
	i, b, v := ast.Int, ast.Bool, ast.Var
	bin := ast.Bin
	div := bin("/", i(1), i(0))
	cases := []struct {
		in, want *ast.Expr
	}{
		{div, div}, // invariant 3: /0 kept
		{bin("*", div, i(0)), bin("*", div, i(0))},                                        // invariant 3: purity guard
		{bin("&&", b(false), bin(">", div, i(0))), b(false)},                              // invariant 2: short-circuit
		{bin("||", b(true), v("x")), b(true)},                                             // invariant 2
		{bin("+", bin("*", v("x"), i(1)), bin("*", i(3), i(4))), bin("+", v("x"), i(12))}, // invariant 1
	}
	for _, c := range cases {
		if got := FoldExpr(c.in); !ast.Equal(got, c.want) {
			return fmt.Errorf("selfcheck: fold of %+v changed semantics", c.in)
		}
	}
	// Rejection leaves no trace: later calls behave normally.
	if got := FoldExpr(bin("+", i(2), i(3))); !ast.Equal(got, i(5)) {
		return fmt.Errorf("selfcheck: state changed after rejection")
	}
	return nil
}
