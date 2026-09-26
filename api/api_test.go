package api

import (
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"

	"ontology/ast"
	"ontology/tac"
)

func fixedCases() []*ast.Expr {
	a, b, c := ast.V("a"), ast.V("b"), ast.V("c")
	return []*ast.Expr{
		ast.Bin("/", ast.Bin("*", a, b), c),
		ast.Bin("&&", ast.Bool(false), ast.Bin(">", ast.Bin("/", ast.Int(1), ast.Int(0)), ast.Int(0))),
		ast.Bin("||", ast.Bool(true), ast.Bin("/", ast.Int(1), ast.Int(0))),
		ast.Bin("||", ast.Bin("&&", a, b), c),
		ast.Bin("+", ast.IfElse(c, ast.Int(1), ast.Int(2)), ast.Int(3)),
		ast.Bin("<=", ast.Neg(a), ast.Bin("+", b, c)),
		ast.IfElse(ast.Bin("&&", ast.Bin(">", a, b), ast.Bool(true)),
			ast.Bin("||", b, c), ast.Bin("/", a, b)),
	}
}

func TestSemanticsMatch(t *testing.T) {
	// cases 1,2 contain /0 only under a dead short-circuit arm: run on env 0.
	envs := []map[string]any{
		{"a": int64(6), "b": int64(3), "c": int64(2)},
		{"a": int64(10), "b": int64(5), "c": int64(2)},
		{"a": int64(0), "b": int64(4), "c": int64(2)},
	}
	check := func(t *testing.T, n *ast.Expr, env map[string]any) {
		t.Helper()
		is, err := tac.Gen(n)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := runTAC(is, env), evalAST(n, env); got != want {
			t.Fatalf("env=%v tac=%v ast=%v\n%s", env, got, want, render(is))
		}
	}
	for i, n := range fixedCases() {
		for j, env := range envs {
			if (i == 1 || i == 2) && j > 0 {
				continue // /0 is dead; one env already proves it never executes
			}
			check(t, n, env)
		}
	}
	for seed := int64(0); seed < 40; seed++ { // random, loop-generated trees
		r := rand.New(rand.NewSource(seed))
		n := randExpr(r, 5, true)
		env := map[string]any{}
		for _, v := range []string{"a", "b", "c"} {
			env[v] = int64(1 + r.Intn(9)) // 1..9: no zero divisors
		}
		check(t, n, env)
	}
}

func randExpr(r *rand.Rand, d int, wantBool bool) *ast.Expr {
	vs := []*ast.Expr{ast.V("a"), ast.V("b"), ast.V("c")}
	pick := func() *ast.Expr { return vs[r.Intn(3)] }
	leaf := func() *ast.Expr {
		if wantBool {
			if r.Intn(2) == 0 {
				return ast.Bool(r.Intn(2) == 0)
			}
			return ast.Bin([]string{"<", ">", "<=", ">=", "==", "!="}[r.Intn(6)], pick(), pick())
		}
		if r.Intn(2) == 0 {
			return ast.Int(int64(1 + r.Intn(9)))
		}
		return pick()
	}
	if d == 0 || r.Intn(3) == 0 {
		return leaf()
	}
	sub := func(b bool) *ast.Expr { return randExpr(r, d-1, b) }
	if wantBool {
		switch r.Intn(4) {
		case 0:
			return ast.Not(sub(true))
		case 1:
			return ast.Bin("&&", sub(true), sub(true))
		case 2:
			return ast.Bin("||", sub(true), sub(true))
		default:
			return ast.IfElse(sub(true), sub(true), sub(true))
		}
	}
	switch r.Intn(3) {
	case 0:
		return ast.Neg(sub(false))
	case 1:
		return ast.IfElse(sub(true), sub(false), sub(false))
	default:
		return ast.Bin([]string{"+", "-", "*"}[r.Intn(3)], sub(false), sub(false))
	}
}

func render(is []tac.Instr) string {
	s := make([]string, len(is))
	for i, x := range is {
		s[i] = x.String()
	}
	return strings.Join(s, "\n")
}

func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentGen(t *testing.T) {
	trees := append(fixedCases(),
		ast.Bin("-", ast.Bin("-", ast.V("a"), ast.V("b")), ast.V("c")),
		ast.Bin("+", ast.V("x"), ast.Bin("*", ast.V("y"), ast.V("z"))))
	const N = 64
	out := make([][][]tac.Instr, N)
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for _, tr := range trees {
				is, err := GenExpr(tr)
				if err != nil {
					t.Errorf("goroutine %d: %v", g, err)
					return
				}
				out[g] = append(out[g], is)
			}
		}(g)
	}
	wg.Wait()
	for g := 1; g < N; g++ {
		if !reflect.DeepEqual(out[g], out[0]) {
			t.Fatalf("goroutine %d produced a different stream", g)
		}
	}
}
