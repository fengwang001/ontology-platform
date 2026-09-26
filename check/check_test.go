package check

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/types"
)

func TestInferTable(t *testing.T) {
	type c struct {
		e   *Expr
		t   types.T
		err error
	}
	cases := []c{
		{IntL(7), types.Int, nil}, {BoolL(true), types.Bool, nil}, {NegE(IntL(1)), types.Int, nil},
		{NegE(BoolL(true)), 0, ErrArithOperand}, {NotE(BoolL(false)), types.Bool, nil}, {NotE(IntL(1)), 0, ErrLogicOperand},
		{BinE("==", IntL(1), IntL(1)), types.Bool, nil}, {BinE("!=", BoolL(true), BoolL(false)), types.Bool, nil},
		{BinE("==", IntL(1), BoolL(true)), 0, ErrEqMismatch}, {IfE(BoolL(true), IntL(1), IntL(2)), types.Int, nil},
		{IfE(IntL(1), IntL(1), IntL(2)), 0, ErrIf}, {IfE(BoolL(true), IntL(1), BoolL(true)), 0, ErrIf},
		{LetE("x", IntL(1), VarE("x")), types.Int, nil}, {LetE("x", IntL(1), LetE("x", BoolL(true), VarE("x"))), types.Bool, nil},
		{BinE("+", LetE("x", IntL(1), VarE("x")), VarE("x")), 0, ErrUndeclared}, {BinE("+", VarE("z"), IntL(1)), 0, ErrUndeclared},
	}
	for _, op := range []string{"+", "-", "*", "/"} {
		cases = append(cases, c{BinE(op, IntL(1), IntL(2)), types.Int, nil}, c{BinE(op, IntL(1), BoolL(true)), 0, ErrArithOperand})
	}
	for _, op := range []string{"<", ">", "<=", ">="} {
		cases = append(cases, c{BinE(op, IntL(1), IntL(2)), types.Bool, nil}, c{BinE(op, BoolL(true), IntL(1)), 0, ErrArithOperand})
	}
	for _, op := range []string{"&&", "||"} {
		cases = append(cases, c{BinE(op, BoolL(true), BoolL(false)), types.Bool, nil}, c{BinE(op, IntL(1), BoolL(true)), 0, ErrLogicOperand})
	}
	for i, tc := range cases {
		got, err := Infer(tc.e, NewEnv())
		bad := tc.err != nil && !errors.Is(err, tc.err)
		bad = bad || (tc.err == nil && (err != nil || got != tc.t))
		if bad {
			t.Errorf("case %d: got %v,%v want %v/%s", i, got, err, tc.err, tc.t)
		}
	}
}

func TestFailureNoTrace(t *testing.T) {
	env := NewEnv()
	bads := []*Expr{VarE("z"), BinE("+", IntL(1), BoolL(true)), BinE("&&", IntL(1), BoolL(true)),
		IfE(IntL(1), IntL(1), IntL(2)), BinE("==", IntL(1), BoolL(true)), LetE("x", IntL(1), VarE("y"))}
	for _, e := range bads {
		if _, err := Infer(e, env); err == nil {
			t.Fatalf("expected rejection of %v", e)
		}
	}
	for _, n := range []string{"x", "y", "z"} {
		if _, ok := env.Lookup(n); ok {
			t.Fatalf("env polluted with %s", n)
		}
	}
	if got, err := Infer(BinE("+", IntL(1), IntL(2)), env); err != nil || got != types.Int {
		t.Fatalf("state leaked after rejection: %v,%v", got, err)
	}
}

func TestProbeCountConstant(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		bind := map[string]types.T{}
		for i := 0; i < m; i++ {
			bind[fmt.Sprintf("n%d", i)] = types.Int
		}
		env := NewEnvFrom(bind)
		env.probes.Store(0)
		_, ok := env.Lookup("n7")
		if got := env.probes.Load(); !ok || got > 3 {
			t.Fatalf("m=%d: ok=%v probes=%d, want found with <= 3", m, ok, got)
		}
	}
}

func TestConcurrentInfer(t *testing.T) {
	shared := NewEnvFrom(map[string]types.T{"a": types.Int, "b": types.Bool})
	exprs := []*Expr{BinE("+", VarE("a"), IntL(1)), NotE(VarE("b")),
		LetE("x", IntL(1), BinE("+", VarE("x"), VarE("a"))), BinE("+", VarE("a"), BoolL(true)), VarE("zz")}
	run := func() []string {
		out := make([]string, len(exprs))
		for i, e := range exprs {
			ty, err := Infer(e, shared)
			out[i] = fmt.Sprintf("%v|%v", ty, err)
		}
		return out
	}
	base := run()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !slices.Equal(run(), base) {
				t.Error("concurrent results diverged")
			}
		}()
	}
	wg.Wait()
}

func TestNaiveAgreement(t *testing.T) {
	cat := func(err error) error {
		for _, s := range []error{ErrUndeclared, ErrArithOperand, ErrLogicOperand, ErrIf, ErrEqMismatch} {
			if errors.Is(err, s) {
				return s
			}
		}
		return nil
	}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 300; i++ {
		e := gen(r, 4, nil)
		gt, ge := Infer(e, NewEnv())
		wt, we := Infer(expand(e), NewEnv())
		if gt != wt || cat(ge) != cat(we) {
			t.Fatalf("iter %d: got %v,%v naive %v,%v", i, gt, ge, wt, we)
		}
	}
}

func gen(r *rand.Rand, depth int, scope []string) *Expr {
	if depth == 0 {
		if len(scope) > 0 && r.Intn(2) == 0 {
			return VarE(scope[r.Intn(len(scope))])
		}
		return []*Expr{IntL(int64(r.Intn(10))), BoolL(r.Intn(2) == 0), VarE("z")}[r.Intn(3)]
	}
	switch r.Intn(4) {
	case 0:
		return NotE(gen(r, depth-1, scope))
	case 1:
		op := []string{"+", "-", "*", "/", "<", ">", "<=", ">=", "==", "!=", "&&", "||"}[r.Intn(12)]
		return BinE(op, gen(r, depth-1, scope), gen(r, depth-1, scope))
	case 2:
		return IfE(gen(r, depth-1, scope), gen(r, depth-1, scope), gen(r, depth-1, scope))
	}
	name := fmt.Sprintf("v%d", r.Intn(3))
	val := IntL(int64(r.Intn(5))) // let 值只用字面量, 避免死代码错误分歧
	if r.Intn(2) == 0 {
		val = BoolL(r.Intn(2) == 0)
	}
	return LetE(name, val, gen(r, depth-1, append(append([]string{}, scope...), name)))
}
