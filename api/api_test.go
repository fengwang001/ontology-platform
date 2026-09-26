package api

import (
	"errors"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/ast"
	"ontology/tac"
)

func TestMatchesReference(t *testing.T) {
	for i, e := range builtins {
		code, _ := GenExpr(e)
		for _, env := range selfEnvs {
			got, _ := tac.Exec(code, env)
			want, _ := ast.Eval(e, env)
			if got != want {
				t.Errorf("expr %d env %v: got %d want %d", i, env, got, want)
			}
		}
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 200; i++ {
		e := randExpr(rng, 4)
		env := map[string]int64{"x": int64(rng.Intn(5) - 2), "y": 1, "z": -1}
		code, _ := GenExpr(e)
		got, gerr := tac.Exec(code, env)
		want, werr := ast.Eval(e, env)
		if (gerr == nil) != (werr == nil) || gerr == nil && got != want {
			t.Fatalf("random %d: exec=(%d,%v) eval=(%d,%v)", i, got, gerr, want, werr)
		}
	}
}

func randExpr(r *rand.Rand, d int) *ast.Expr {
	if d == 0 || r.Intn(4) == 0 {
		return []*ast.Expr{ast.Int(int64(r.Intn(5))), ast.Bool(r.Intn(2) == 0), ast.Var("x")}[r.Intn(3)]
	}
	l, rr := randExpr(r, d-1), randExpr(r, d-1)
	switch r.Intn(3) {
	case 0:
		return ast.Logic([]string{"&&", "||"}[r.Intn(2)], l, rr)
	case 1:
		return ast.If(l, rr, randExpr(r, d-1))
	default:
		return ast.Bin([]string{"+", "-", "*", "/", "<", "=="}[r.Intn(6)], l, rr)
	}
}

func TestShortCircuitSkipsRight(t *testing.T) {
	cases := []struct {
		e    *ast.Expr
		want int64
	}{
		{ast.Logic("&&", ast.Bool(false), div10), 0},
		{ast.Logic("||", ast.Bool(true), div10), 1},
		{ast.Logic("&&", ast.Var("x"), div10), 0},
	}
	for i, c := range cases {
		code, _ := GenExpr(c.e)
		got, err := tac.Exec(code, map[string]int64{"x": 0})
		if err != nil || got != c.want {
			t.Errorf("case %d: got (%d,%v) want (%d,nil)", i, got, err, c.want)
		}
	}
	code, _ := GenExpr(ast.Logic("&&", ast.Bool(true), div10))
	if _, err := tac.Exec(code, nil); err == nil {
		t.Error("reachable 1/0 must error at run time")
	}
}

func TestTempsSequentialResultLast(t *testing.T) {
	for i, e := range builtins {
		code, _ := GenExpr(e)
		if bad := checkTemps(code); bad != "" {
			t.Errorf("expr %d: %s", i, bad)
		}
	}
}
func TestErrorsDistinct(t *testing.T) {
	s := []error{tac.ErrNilNode, tac.ErrUnknownOp, tac.ErrMissingBranch}
	for i := range s {
		for j := range s {
			if i != j && errors.Is(s[i], s[j]) {
				t.Errorf("sentinels %d,%d not distinct", i, j)
			}
		}
	}
}
func TestRejectionLeavesNoTrace(t *testing.T) {
	cases := []struct {
		e    *ast.Expr
		want error
	}{
		{nil, tac.ErrNilNode},
		{ast.Bin("%", ast.Int(1), ast.Int(2)), tac.ErrUnknownOp},
		{ast.If(ast.Bool(true), nil, ast.Int(2)), tac.ErrMissingBranch},
	}
	for i, c := range cases {
		if code, err := GenExpr(c.e); code != nil || !errors.Is(err, c.want) {
			t.Errorf("case %d: got (%v,%v) want (nil,%v)", i, code, err, c.want)
		}
	}
	for i := 0; i < 3; i++ {
		if code, err := GenExpr(ast.Var("v")); err != nil || len(code) != 1 || code[0].Dst != "t1" {
			t.Fatalf("call %d after rejections: (%v,%v)", i, code, err)
		}
	}
}
func TestLivePeakConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		e := ast.Var("v")
		for i := 1; i < m; i++ {
			e = ast.Bin("-", e, ast.Var("v"))
		}
		code, err := GenExpr(e)
		if err != nil || len(code) != m-1 {
			t.Fatalf("m=%d: (%d,%v)", m, len(code), err)
		}
		if p := livePeak(code); p > 2 {
			t.Errorf("m=%d: live temp peak %d > 2", m, p)
		}
	}
}
func TestConcurrentGenIdentical(t *testing.T) {
	got := make([][][]tac.Instr, 8)
	var wg sync.WaitGroup
	for g := range got {
		got[g] = make([][]tac.Instr, len(builtins))
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i, e := range builtins {
				got[g][i], _ = GenExpr(e)
			}
		}(g)
	}
	wg.Wait()
	for g := 1; g < len(got); g++ {
		for i := range got[g] {
			if !slices.Equal(got[g][i], got[0][i]) {
				t.Errorf("goroutine %d expr %d differs", g, i)
			}
		}
	}
}
