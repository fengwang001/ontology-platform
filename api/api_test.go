package api

import (
	"errors"
	"math/rand"
	"ontology/ast"
	"sync"
	"sync/atomic"
	"testing"
)

var (
	errDivZero = errors.New("division by zero")
	iops       = map[string]func(int64, int64) int64{
		"+": func(a, b int64) int64 { return a + b }, "-": func(a, b int64) int64 { return a - b },
		"*": func(a, b int64) int64 { return a * b }, "/": func(a, b int64) int64 { return a / b },
	}
	cops = map[string]func(int64, int64) bool{
		"<": func(a, b int64) bool { return a < b }, ">": func(a, b int64) bool { return a > b },
		"<=": func(a, b int64) bool { return a <= b }, ">=": func(a, b int64) bool { return a >= b },
		"==": func(a, b int64) bool { return a == b }, "!=": func(a, b int64) bool { return a != b },
	}
)

// eval is the naive reference: substitute vars, evaluate; /0 is an error.
func eval(n *ast.Expr, env map[string]*ast.Expr) (v *ast.Expr, err error) {
	defer func() {
		if recover() != nil {
			v, err = nil, errDivZero
		}
	}()
	return ev(n, env), nil
}
func ev(n *ast.Expr, env map[string]*ast.Expr) *ast.Expr {
	switch n.Kind {
	case ast.KInt, ast.KBool:
		return n
	case ast.KVar:
		return env[n.Name]
	case ast.KIf:
		if ev(n.C, env).Bool {
			return ev(n.T, env)
		}
		return ev(n.E, env)
	}
	l := ev(n.L, env)
	if n.Op == "&&" || n.Op == "||" {
		if (n.Op == "&&") != l.Bool {
			return ast.Bool(l.Bool)
		}
		return ev(n.R, env)
	}
	r := ev(n.R, env)
	if n.Op == "/" && r.Int == 0 {
		panic(errDivZero)
	}
	if f, ok := iops[n.Op]; ok {
		return ast.Int(f(l.Int, r.Int))
	}
	if f, ok := cops[n.Op]; ok {
		return ast.Bool(f(l.Int, r.Int))
	}
	panic("bad op")
}

// gen builds a random well-typed expression of depth d.
func gen(r *rand.Rand, d int, isInt bool) *ast.Expr {
	if d <= 0 {
		if isInt {
			return []*ast.Expr{ast.Int(int64(r.Intn(5) - 2)), ast.Var("x"), ast.Var("y")}[r.Intn(3)]
		}
		return []*ast.Expr{ast.Bool(true), ast.Bool(false), ast.Var("b")}[r.Intn(3)]
	}
	if r.Intn(4) == 0 {
		return ast.If(gen(r, d-1, false), gen(r, d-1, isInt), gen(r, d-1, isInt))
	}
	if isInt {
		return ast.Bin([]string{"+", "-", "*", "/"}[r.Intn(4)], gen(r, d-1, true), gen(r, d-1, true))
	}
	if r.Intn(2) == 0 {
		return ast.Bin([]string{"&&", "||"}[r.Intn(2)], gen(r, d-1, false), gen(r, d-1, false))
	}
	return ast.Bin([]string{"<", ">", "<=", ">=", "==", "!="}[r.Intn(6)], gen(r, d-1, true), gen(r, d-1, true))
}
func TestFaultInjection(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
	bad := []*ast.Expr{ast.Not(nil), ast.Bin("%", ast.Int(1), ast.Int(2)), ast.Bin("+", ast.Int(1), ast.Bool(true))}
	want := []error{ErrNilNode, ErrUnknownOp, ErrTypeMismatch}
	for i := range bad {
		if err := validate(bad[i]); !errors.Is(err, want[i]) {
			t.Fatalf("case %d: want %v, got %v", i, want[i], err)
		}
	}
	if ErrNilNode == ErrUnknownOp || ErrUnknownOp == ErrTypeMismatch {
		t.Fatal("sentinel errors must be distinct")
	}
	if got := FoldExpr(ast.Bin("+", ast.Int(2), ast.Int(3))); !ast.Equal(got, ast.Int(5)) {
		t.Fatal("state changed after rejection")
	}
}
func TestRefConsistency(t *testing.T) {
	r := rand.New(rand.NewSource(662))
	envs := []map[string]*ast.Expr{
		{"x": ast.Int(-1), "y": ast.Int(2), "b": ast.Bool(false)},
		{"x": ast.Int(0), "y": ast.Int(2), "b": ast.Bool(true)},
		{"x": ast.Int(3), "y": ast.Int(0), "b": ast.Bool(false)},
	}
	for i := 0; i < 60; i++ {
		tree := gen(r, 4, i%2 == 0)
		orig := ast.Copy(tree)
		folded := FoldExpr(tree)
		if !ast.Equal(tree, orig) {
			t.Fatal("FoldExpr mutated its input")
		}
		for _, env := range envs {
			v1, e1 := eval(orig, env)
			v2, e2 := eval(folded, env)
			if (e1 == nil) != (e2 == nil) || (e1 == nil && !ast.Equal(v1, v2)) {
				t.Fatalf("tree %d: semantics changed", i)
			}
		}
	}
}
func TestConcurrentFold(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	batch, want := make([]*ast.Expr, 16), make([]*ast.Expr, 16)
	for i := range batch {
		batch[i] = gen(r, 4, i%2 == 0)
		want[i] = FoldExpr(batch[i])
	}
	var wg sync.WaitGroup
	var bad atomic.Bool
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, e := range batch {
				if !ast.Equal(FoldExpr(e), want[i]) {
					bad.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	if bad.Load() {
		t.Fatal("concurrent folds differ")
	}
}
