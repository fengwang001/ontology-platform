// Package api is the public entry point: FoldExpr and SelfCheck.
package api

import "errors"
import "fmt"
import "ontology/ast"
import "ontology/fold"

var ErrNilNode = fold.ErrNilNode
var ErrUnknownOp = fold.ErrUnknownOp
var ErrTypeMismatch = fold.ErrTypeMismatch
var errRuntime = errors.New("api: runtime evaluation error (division by zero or bad type)")

// foldErr converts the sentinel panic of fold.Fold into a returned error.
func foldErr(n *ast.Expr) (out *ast.Expr, err error) {
	defer func() {
		if e, ok := recover().(error); ok {
			err = e
		}
	}()
	return fold.Fold(n), nil
}

// FoldExpr returns a fresh folded copy; stateless, concurrency-safe; nil on invalid input.
func FoldExpr(n *ast.Expr) *ast.Expr { out, _ := foldErr(n); return out }

// eval is the naive reference: literal node or errRuntime; ev panics, eval converts.
func eval(n *ast.Expr, env map[string]*ast.Expr) (out *ast.Expr, err error) {
	defer func() {
		if recover() != nil {
			out, err = nil, errRuntime
		}
	}()
	return ev(n, env), nil
}
func ev(n *ast.Expr, env map[string]*ast.Expr) *ast.Expr {
	switch n.Kind {
	case ast.KInt, ast.KBool:
		return n
	case ast.KVar:
		v := env[n.Name]
		if v == nil {
			panic(errRuntime)
		}
		return v
	case ast.KNeg, ast.KNot:
		v := ev(n.Sub, env)
		neg := n.Kind == ast.KNeg
		if v.Kind != map[bool]ast.Kind{true: ast.KInt, false: ast.KBool}[neg] {
			panic(errRuntime)
		}
		if neg {
			return ast.Int(-v.I)
		}
		return ast.Bool(!v.B)
	case ast.KIf:
		c := ev(n.C, env)
		if c.Kind != ast.KBool {
			panic(errRuntime)
		}
		return ev(map[bool]*ast.Expr{true: n.T, false: n.E}[c.B], env)
	}
	return evBin(n, env)
}
func evBin(n *ast.Expr, env map[string]*ast.Expr) *ast.Expr {
	if n.Op == "&&" || n.Op == "||" {
		l := ev(n.L, env) // reference short-circuit: RHS only when needed
		if l.Kind != ast.KBool {
			panic(errRuntime)
		}
		if (n.Op == "&&" && !l.B) || (n.Op == "||" && l.B) {
			return ast.Bool(n.Op == "||")
		}
		r := ev(n.R, env)
		if r.Kind != ast.KBool {
			panic(errRuntime)
		}
		return r
	}
	l, r := ev(n.L, env), ev(n.R, env)
	if l.Kind != ast.KInt || r.Kind != ast.KInt {
		panic(errRuntime)
	}
	switch n.Op {
	case "+":
		return ast.Int(l.I + r.I)
	case "-":
		return ast.Int(l.I - r.I)
	case "*":
		return ast.Int(l.I * r.I)
	case "/":
		if r.I == 0 {
			panic(errRuntime)
		}
		return ast.Int(l.I / r.I)
	}
	cmp, ok := map[string]bool{"<": l.I < r.I, ">": l.I > r.I, "<=": l.I <= r.I,
		">=": l.I >= r.I, "==": l.I == r.I, "!=": l.I != r.I}[n.Op]
	if !ok {
		panic(errRuntime)
	}
	return ast.Bool(cmp)
}

// SelfCheck verifies the four invariants over built-in ASTs; nil means pass.
func SelfCheck() error {
	x, y := ast.Var("x"), ast.Var("y")
	d0 := ast.Bin("/", ast.Int(1), ast.Int(0))
	big := ast.Bin("+", ast.Bin("+", ast.Bin("*", x, ast.Int(1)), ast.Bin("*", y, ast.Int(0))),
		ast.Bin("*", ast.Int(3), ast.Int(4)))
	for _, c := range [][2]*ast.Expr{
		{big, ast.Bin("+", x, ast.Int(12))}, // six-step derivation; invariant 1
		{d0, d0},                            // invariant 3: /0 survives
		{ast.Bin("&&", ast.Bool(false), ast.Bin(">", d0, ast.Int(0))), ast.Bool(false)}, // inv 2
		{ast.Bin("*", d0, ast.Int(0)), ast.Bin("*", d0, ast.Int(0))},                    // inv 3 vs identity
		{ast.Bin("||", ast.Bool(true), x), ast.Bool(true)},
		{ast.Bin("*", ast.Bin("+", ast.Int(2), ast.Int(3)), ast.Int(4)), ast.Int(20)},
	} {
		if got := FoldExpr(c[0]); !ast.Equal(got, c[1]) {
			return fmt.Errorf("SelfCheck fixed case: got %s want %s", got, c[1])
		}
	}
	if got := FoldExpr(ast.Bin("||", ast.Bool(true), ast.Bin("?", ast.Int(1), ast.Int(2)))); !ast.Equal(got, ast.Bool(true)) {
		return errors.New("SelfCheck: short circuit touched invalid right side") // inv 2
	}
	for _, e := range []*ast.Expr{ // inv 1: identical result-or-error under empty env
		ast.Bin("*", ast.Bin("+", ast.Int(2), ast.Int(3)), ast.Int(4)),
		d0, ast.Bin("*", d0, ast.Int(0)), ast.Bin("/", ast.Int(6), ast.Int(2)),
	} {
		v1, e1 := eval(e, nil)
		v2, e2 := eval(FoldExpr(e), nil)
		if (e1 != nil) != (e2 != nil) || (e1 == nil && !ast.Equal(v1, v2)) {
			return fmt.Errorf("SelfCheck semantic: %s changed meaning", e)
		}
	}
	bad := []*ast.Expr{nil, ast.Bin("%", ast.Int(1), ast.Int(2)), ast.Bin("+", ast.Int(1), ast.Bool(true))}
	for i, want := range []error{ErrNilNode, ErrUnknownOp, ErrTypeMismatch} { // inv 4
		if _, err := foldErr(bad[i]); !errors.Is(err, want) {
			return fmt.Errorf("SelfCheck sentinel: got %v want %v", err, want)
		}
	}
	if !ast.Equal(FoldExpr(ast.Bin("+", ast.Int(1), ast.Int(2))), ast.Int(3)) {
		return errors.New("SelfCheck: state affected by rejected inputs") // inv 4
	}
	return nil
}
