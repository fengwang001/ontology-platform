package api

import "errors"
import "math/rand"
import "sync"
import "testing"
import "ontology/ast"

// genTree builds a well-typed random AST (0=int,1=bool); divisors are possibly-zero literals or variables, int substitutions are non-zero.
func genTree(r *rand.Rand, d, t int) *ast.Expr {
	if d == 0 || r.Intn(3) == 0 {
		if t == 1 {
			return map[int]*ast.Expr{0: ast.Var("p"), 1: ast.Bool(r.Intn(2) == 0)}[r.Intn(2)]
		}
		return map[int]*ast.Expr{0: ast.Var([]string{"x", "y"}[r.Intn(2)]),
			1: ast.Int(int64(r.Intn(5) - 2))}[r.Intn(2)]
	}
	g := func(t int) *ast.Expr { return genTree(r, d-1, t) }
	if t == 1 {
		switch r.Intn(4) {
		case 0:
			return ast.Not(g(1))
		case 1:
			return ast.If(g(1), g(1), g(1))
		case 2:
			return ast.Bin([]string{"<", ">", "<=", ">=", "==", "!="}[r.Intn(6)], g(0), g(0))
		default:
			return ast.Bin([]string{"&&", "||"}[r.Intn(2)], g(1), g(1))
		}
	}
	if r.Intn(2) == 0 {
		return ast.Neg(g(0))
	}
	if r.Intn(2) == 0 {
		return ast.If(g(1), g(0), g(0))
	}
	op := []string{"+", "-", "*", "/"}[r.Intn(4)]
	rr := g(0)
	if op == "/" {
		rr = map[bool]*ast.Expr{true: ast.Int(int64(r.Intn(4) - 1)), false: ast.Var([]string{"x", "y"}[r.Intn(2)])}[r.Intn(2) == 0]
	}
	return ast.Bin(op, g(0), rr)
}
func forEachEnv(fn func(map[string]*ast.Expr)) { // 18 combos; ints are non-zero
	for i := 0; i < 18; i++ {
		fn(map[string]*ast.Expr{"x": ast.Int([]int64{-2, -1, 1}[i/6%3]),
			"y": ast.Int([]int64{-2, -1, 1}[i/2%3]), "p": ast.Bool(i%2 == 0)})
	}
}
func TestFoldCases(t *testing.T) { // pins each rule's output + input non-mutation
	x, d0 := ast.Var("x"), ast.Bin("/", ast.Int(1), ast.Int(0))
	big := ast.Bin("+", ast.Bin("+", ast.Bin("*", x, ast.Int(1)), ast.Bin("*", ast.Var("y"), ast.Int(0))),
		ast.Bin("*", ast.Int(3), ast.Int(4)))
	snap := ast.DeepCopy(big)
	for i, c := range [][2]*ast.Expr{
		{big, ast.Bin("+", x, ast.Int(12))}, {ast.Neg(ast.Int(7)), ast.Int(-7)},
		{ast.Bin("<", ast.Int(1), ast.Int(2)), ast.Bool(true)},
		{ast.If(ast.Bool(true), ast.Int(1), ast.Int(2)), ast.Int(1)},
		{ast.Bin("+", x, ast.Int(0)), x}, {ast.Bin("*", ast.Int(1), x), x},
		{ast.Bin("/", x, ast.Int(1)), x}, {ast.Bin("*", ast.Int(0), x), ast.Int(0)},
		{ast.Bin("*", x, ast.Int(0)), ast.Int(0)}, {d0, d0},
		{ast.Bin("&&", ast.Bool(false), ast.Bin(">", d0, ast.Int(0))), ast.Bool(false)},
		{ast.Bin("||", ast.Bool(true), x), ast.Bool(true)},
		{ast.Bin("*", d0, ast.Int(0)), ast.Bin("*", d0, ast.Int(0))},
		{ast.Bin("*", ast.Bin("+", ast.Int(2), ast.Int(3)), ast.Int(4)), ast.Int(20)},
	} {
		if got := FoldExpr(c[0]); !ast.Equal(got, c[1]) {
			t.Errorf("case %d: got %s want %s", i, got, c[1])
		}
	}
	if !ast.Equal(big, snap) {
		t.Fatalf("input mutated")
	}
}
func TestShortCircuit(t *testing.T) { // invariant 2: RHS is never entered
	for _, c := range [][2]*ast.Expr{
		{ast.Bin("&&", ast.Bool(false), nil), ast.Bool(false)},
		{ast.Bin("||", ast.Bool(true), nil), ast.Bool(true)},
		{ast.Bin("||", ast.Bool(true), ast.Bin("?", ast.Int(1), ast.Int(2))), ast.Bool(true)},
		{ast.Bin("&&", ast.Bool(false), ast.Bin("/", ast.Int(1), ast.Int(0))), ast.Bool(false)},
	} {
		if got := FoldExpr(c[0]); !ast.Equal(got, c[1]) {
			t.Errorf("got %s want %s", got, c[1])
		}
	}
}
func TestDivZero(t *testing.T) { // invariant 3: a constant-zero divisor survives
	for _, in := range []*ast.Expr{
		ast.Bin("/", ast.Int(1), ast.Int(0)),
		ast.Bin("*", ast.Bin("/", ast.Int(1), ast.Int(0)), ast.Int(0)),
	} {
		if got := FoldExpr(in); !ast.Equal(got, in) {
			t.Errorf("div0 must survive: got %s", got)
		}
	}
}
func TestSentinels(t *testing.T) { // invariant 4: three distinct, judgeable errors
	bad := []*ast.Expr{nil, ast.Bin("%", ast.Int(1), ast.Int(2)), ast.Bin("+", ast.Int(1), ast.Bool(true))}
	for i, want := range []error{ErrNilNode, ErrUnknownOp, ErrTypeMismatch} {
		if _, err := foldErr(bad[i]); !errors.Is(err, want) {
			t.Fatalf("case %d: got %v want %v", i, err, want)
		}
	}
	if !ast.Equal(FoldExpr(ast.Bin("+", ast.Int(1), ast.Int(2))), ast.Int(3)) {
		t.Fatalf("later call affected by rejected inputs")
	}
}
func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
func TestSemanticEquivalence(t *testing.T) { // invariant 1 over many random ASTs
	for seed := int64(1); seed <= 80; seed++ {
		root := genTree(rand.New(rand.NewSource(seed)), 5, int(seed%2))
		out := FoldExpr(root)
		forEachEnv(func(env map[string]*ast.Expr) {
			v1, e1 := eval(root, env)
			v2, e2 := eval(out, env)
			if (e1 != nil) != (e2 != nil) || (e1 == nil && !ast.Equal(v1, v2)) {
				t.Fatalf("seed %d: %v/%v vs %v/%v", seed, v1, e1, v2, e2)
			}
		})
	}
}
func TestConcurrent(t *testing.T) { // N goroutines fold one read-only batch
	batch := make([]*ast.Expr, 12)
	for i := range batch {
		batch[i] = genTree(rand.New(rand.NewSource(int64(100+i))), 5, i%2)
	}
	const n = 16
	res := make([][]*ast.Expr, n)
	var wg sync.WaitGroup
	for g := range res {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for _, b := range batch {
				res[g] = append(res[g], FoldExpr(b))
			}
		}(g)
	}
	wg.Wait()
	for i := 1; i < n*len(batch); i++ {
		if !ast.Equal(res[0][i%len(batch)], res[i/len(batch)][i%len(batch)]) {
			t.Fatalf("concurrent result %d differs", i)
		}
	}
}
