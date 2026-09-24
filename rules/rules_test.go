package rules_test

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/ast"
	"ontology/equiv"
	"ontology/push"
	"ontology/rules"
)

var cols3 = []string{"t.a", "t.b", "t.c"}

func col(name string) *ast.Node { return ast.Col("t", name) }

// TestStructuralRulesEquiv 每条结构规则在 3^3=27 种赋值下全量验证等价。
func TestStructuralRulesEquiv(t *testing.T) {
	a, b := col("a"), col("b")
	cases := []struct {
		name string
		lhs  *ast.Node
		rhs  *ast.Node
	}{
		{"double-negation", ast.Not1(ast.Not1(a)), a},
		{"de-morgan-and", ast.Not1(ast.AndN(a, b)), ast.OrN(ast.Not1(a), ast.Not1(b))},
		{"de-morgan-or", ast.Not1(ast.OrN(a, b)), ast.AndN(ast.Not1(a), ast.Not1(b))},
	}
	for _, c := range cases {
		if bad := equiv.Check(c.lhs, c.rhs, cols3); bad != nil {
			t.Fatalf("rule %s not equivalent at %s", c.name, bad)
		}
	}
}

// TestRandomRewriteEquiv 200 棵随机树：重写前后等价、轮数与访问量有界。
func TestRandomRewriteEquiv(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	totBefore, totAfter, totRounds, fails := 0, 0, 0, 0
	for i := 0; i < 200; i++ {
		tree := rules.GenRandom(rng, cols3, 5)
		out, st, err := rules.Rewrite(tree, rules.Default())
		if err != nil {
			t.Fatalf("tree %d: %v", i, err)
		}
		if bad := equiv.Check(tree, out, cols3); bad != nil {
			fails++
			t.Fatalf("tree %d not equivalent at %s", i, bad)
		}
		if st.Rounds > 4*tree.Size() {
			t.Fatalf("tree %d: rounds %d > 4*size %d", i, st.Rounds, 4*tree.Size())
		}
		if st.Visits > st.Rounds*tree.Size()*4 {
			t.Fatalf("tree %d: visits %d > bound", i, st.Visits)
		}
		totBefore += tree.Size()
		totAfter += out.Size()
		totRounds += st.Rounds
	}
	t.Logf("stats: avgSizeBefore=%.1f avgSizeAfter=%.1f avgRounds=%.2f equivFails=%d",
		float64(totBefore)/200, float64(totAfter)/200, float64(totRounds)/200, fails)
}

// TestOscillation 故意加入互逆规则，断言检出震荡并报可判定错误。
func TestOscillation(t *testing.T) {
	rs := append(rules.Default(), rules.Rule{
		Name: "neg-intro",
		Local: func(n *ast.Node) (*ast.Node, bool) {
			if n.Kind == ast.Column {
				return ast.Not1(ast.Not1(n)), true
			}
			return nil, false
		},
	})
	_, _, err := rules.Rewrite(col("a"), rs)
	if !errors.Is(err, rules.ErrOscillation) {
		t.Fatalf("want ErrOscillation, got %v", err)
	}
}

// TestDeterminism 打乱规则顺序 20 次，结果逐字节一致。
func TestDeterminism(t *testing.T) {
	base := ast.Not1(ast.AndN(col("a"), ast.OrN(col("b"), ast.K(ast.Unknown))))
	want, _, err := rules.Rewrite(base, rules.Default())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		rs := rules.Default()
		rand.New(rand.NewSource(int64(i))).Shuffle(len(rs), func(x, y int) {
			rs[x], rs[y] = rs[y], rs[x]
		})
		got, _, err := rules.Rewrite(base, rs)
		if err != nil || got.String() != want.String() {
			t.Fatalf("shuffle %d: %v %s != %s", i, err, got, want)
		}
	}
}

// TestBoundaries 空树、单常量、深度 1000 链、重复子表达式、全 NULL。
func TestBoundaries(t *testing.T) {
	if _, _, err := rules.Rewrite(nil, rules.Default()); !errors.Is(err, ast.ErrEmpty) {
		t.Fatalf("empty tree: want ErrEmpty, got %v", err)
	}
	out, _, err := rules.Rewrite(ast.K(ast.Unknown), rules.Default())
	if err != nil || out.String() != "NULL" {
		t.Fatalf("single constant: %v %v", out, err)
	}
	chain := col("a")
	for i := 0; i < 1000; i++ {
		chain = ast.Not1(chain)
	}
	out, st, err := rules.Rewrite(chain, rules.Default())
	if err != nil || out.String() != "t.a" {
		t.Fatalf("deep chain: %v %v", out, err)
	}
	if st.Rounds > 4*1001 {
		t.Fatalf("deep chain rounds %d", st.Rounds)
	}
	dup := ast.AndN(col("a"), col("a"), ast.Not1(col("a")))
	out, _, err = rules.Rewrite(dup, rules.Default())
	if err != nil || out.String() != "FALSE" {
		t.Fatalf("duplicate subexpr: %v %v", out, err)
	}
	out, _, err = rules.Rewrite(ast.AndN(ast.K(ast.Unknown), ast.K(ast.Unknown)), rules.Default())
	if err != nil || out.String() != "NULL" {
		t.Fatalf("all NULL: %v %v", out, err)
	}
}

// TestPush 谓词下推：单表项入 Scan、多表项与常量留顶层、未知表报错、
// 下推后无跨表引用，且下推前后整体谓词等价。
func TestPush(t *testing.T) {
	oc := func(t, c string) *ast.Node { return ast.Col(t, c) }
	where := ast.AndN(oc("o", "a"), oc("c", "b"),
		ast.Eq2(oc("o", "id"), oc("c", "oid")), ast.K(ast.True))
	plan := &push.Plan{Where: where,
		Scans: []*push.Scan{{Table: "o"}, {Table: "c"}}}
	if err := push.Push(plan); err != nil {
		t.Fatal(err)
	}
	if err := push.Check(plan); err != nil {
		t.Fatalf("cross-table ref remains: %v", err)
	}
	if plan.Scans[0].Pred.String() != "o.a" || plan.Scans[1].Pred.String() != "c.b" {
		t.Fatalf("pushed preds: %s / %s", plan.Scans[0].Pred, plan.Scans[1].Pred)
	}
	joined := ast.AndN(plan.Where, plan.Scans[0].Pred, plan.Scans[1].Pred)
	if bad := equiv.Check(where, joined, nil); bad != nil {
		t.Fatalf("push changed semantics at %s", bad)
	}
	bad := &push.Plan{Where: oc("ghost", "x"),
		Scans: []*push.Scan{{Table: "o"}}}
	if err := push.Push(bad); !errors.Is(err, push.ErrUnknownTable) {
		t.Fatalf("unknown table: want ErrUnknownTable, got %v", err)
	}
	if err := push.Push(&push.Plan{}); !errors.Is(err, ast.ErrEmpty) {
		t.Fatalf("empty plan: want ErrEmpty, got %v", err)
	}
}
