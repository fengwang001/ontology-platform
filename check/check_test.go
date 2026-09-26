package check

import (
	"errors"
	"math/rand/v2"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/types"
)

var (
	errCode = map[error]string{ErrUndefined: "undef", ErrIntRequired: "intneed", ErrBoolRequired: "boolneed", ErrIf: "if", ErrEqMismatch: "eq"}
	base    = map[string]types.T{"a": types.Int, "b": types.Bool, "c": types.Int}
	ops     = []string{"+", "-", "*", "/", "<", ">", "<=", ">=", "==", "!=", "&&", "||"}
)

type gen struct {
	r *rand.Rand
	n int
}

func code(t types.T, err error) string {
	if err == nil {
		return t.String()
	}
	for e, s := range errCode {
		if errors.Is(err, e) {
			return s
		}
	}
	return "other"
}
func vinf(e *Expr, env *Env) string { t, err := Infer(e, env); return code(t, err) }
func want(t *testing.T, e *Expr, env *Env, w string) {
	if g := vinf(e, env); g != w {
		t.Errorf("want %s got %s", w, g)
	}
}

func TestRules(t *testing.T) {
	for _, c := range [][2]any{
		{L(1), "int"}, {B(true), "bool"}, {U("-", L(1)), "int"},
		{U("-", B(true)), "intneed"}, {U("!", B(false)), "bool"}, {U("!", L(1)), "boolneed"},
		{Bin("+", L(1), L(2)), "int"}, {Bin("*", L(1), B(true)), "intneed"},
		{Bin(">", L(1), B(true)), "intneed"}, {Bin("==", L(1), L(2)), "bool"},
		{Bin("==", L(1), B(true)), "eq"}, {Bin("&&", B(true), B(false)), "bool"},
		{Bin("||", B(true), L(1)), "boolneed"}, {If(L(1), L(1), L(2)), "if"},
		{If(B(true), L(1), B(true)), "if"}, {Bin("+", V("z"), L(1)), "undef"},
		{Let("x", L(1), V("x")), "int"},
	} {
		want(t, c[0].(*Expr), NewEnv(), c[1].(string))
	}
}
func TestLetScope(t *testing.T) {
	env := NewEnv()
	want(t, Let("x", L(1), V("x")), env, "int")
	want(t, V("x"), env, "undef")
	env.Bind("x", types.Bool)
	want(t, Let("x", L(1), V("x")), env, "int")
	want(t, V("x"), env, "bool")
}
func TestIfBranches(t *testing.T) {
	want(t, If(B(true), L(1), B(true)), NewEnv(), "if")
	want(t, If(L(0), L(1), L(2)), NewEnv(), "if")
}
func TestFailureLeavesNoTrace(t *testing.T) {
	env := NewEnv()
	env.Bind("y", types.Int)
	for _, e := range []*Expr{
		Bin("+", V("z"), L(1)), Bin("<", L(1), B(true)), Bin("&&", B(true), L(1)),
		If(L(1), L(1), L(2)), Bin("==", L(1), B(true)), U("!", L(1)),
	} {
		if _, err := Infer(e, env); err == nil {
			t.Fatal("invalid expression accepted")
		}
	}
	want(t, V("y"), env, "int")
	want(t, V("z"), env, "undef")
}

func (g *gen) expr(d int, vs []string) *Expr {
	if d == 0 || g.r.IntN(3) == 0 {
		if len(vs) > 0 && g.r.IntN(2) == 0 {
			return V(vs[g.r.IntN(len(vs))])
		}
		if g.r.IntN(2) == 0 {
			return L(int64(g.r.IntN(7)) - 3)
		}
		return B(g.r.IntN(2) == 0)
	}
	a, b := g.expr(d-1, vs), g.expr(d-1, vs)
	switch g.r.IntN(6) {
	case 2:
		return Bin(ops[g.r.IntN(len(ops))], a, b)
	case 3:
		return If(a, b, g.expr(d-1, vs))
	case 0, 1:
		return U([]string{"-", "!"}[g.r.IntN(2)], a)
	}
	g.n++
	nm := "l" + strconv.Itoa(g.n)
	return Let(nm, a, g.expr(d-1, append(vs, nm)))
}
func TestReferenceEquivalence(t *testing.T) {
	env := &Env{leaf: &frame{bind: base}}
	for s := int64(0); s < 200; s++ {
		g := &gen{r: rand.New(rand.NewPCG(uint64(s), uint64(s+1)))}
		e := g.expr(4, []string{"a", "b", "c"})
		if code(Infer(e, env)) != code(NaiveInfer(e, base)) {
			t.Fatalf("seed %d mismatch", s)
		}
	}
}
func TestLookupProbeConstant(t *testing.T) {
	// Multi-size m=100..10000 and the constant-bound assertion live in
	// VerifyLookupProbe; the raw probe count stays unexported.
	if err := VerifyLookupProbe(); err != nil {
		t.Fatal(err)
	}
}
func TestConcurrent(t *testing.T) {
	g := &gen{r: rand.New(rand.NewPCG(7, 8))}
	env := &Env{leaf: &frame{bind: map[string]types.T{"a": types.Int, "b": types.Bool}}}
	es := make([]*Expr, 48)
	w0 := make([]string, 48)
	for i := range es {
		es[i] = g.expr(4, []string{"a", "b"})
		w0[i] = vinf(es[i], env)
	}
	var bad atomic.Bool
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, e := range es {
				if vinf(e, env) != w0[i] {
					bad.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	if bad.Load() {
		t.Fatal("concurrent verdicts diverged")
	}
}
