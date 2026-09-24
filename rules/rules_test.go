package rules_test

import (
	"math/rand"
	"strings"
	"testing"

	"ontology/ast"
	"ontology/equiv"
	"ontology/rules"
)

func TestRandomFixpoint(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	cols := []string{"t1.a", "t1.b", "t2.c", "t2.d"}
	ineq, fails, sumDelta, sumRounds := 0, 0, 0, 0
	for i := 0; i < 200; i++ {
		tree := rules.Random(r, cols, 5)
		out, st, err := rules.Fixpoint(tree, rules.Std())
		if err != nil {
			t.Fatalf("tree %d: %v", i, err)
		}
		if eq, ce := equiv.FilterEqual(tree, out); !eq {
			t.Errorf("tree %d not filter-equivalent at %s", i, ce)
			ineq++
		}
		if st.Rounds > 4*ast.Size(tree) {
			t.Errorf("tree %d: rounds %d > 4*size %d", i, st.Rounds, ast.Size(tree))
		}
		if st.Visits > 4*st.Rounds*st.MaxSize {
			t.Errorf("tree %d: visits %d > 4*rounds*maxsize", i, st.Visits)
		}
		fails += ineq
		sumDelta += ast.Size(out) - ast.Size(tree)
		sumRounds += st.Rounds
	}
	t.Logf("200 trees: equiv failures=%d avg node delta=%.2f avg rounds=%.2f",
		ineq, float64(sumDelta)/200, float64(sumRounds)/200)
}

func TestBoundaries(t *testing.T) {
	deep := ast.Col("t", "x")
	for i := 0; i < 1000; i++ {
		deep = ast.Not(deep)
	}
	cases := []struct {
		name    string
		tree    *ast.Node
		wantErr string
		want    string
	}{
		{"empty tree", nil, "empty tree", ""},
		{"zero-kid AND", ast.And(), "no kids", ""},
		{"single const", ast.Const(ast.True), "", "true"},
		{"all NULL consts", ast.And(ast.Const(ast.Unknown), ast.Const(ast.Unknown)), "", "unk"},
		{"depth-1000 NOT chain", deep, "", "t.x"},
		{"degenerate 1-kid OR", ast.Or(ast.Col("t", "y")), "", "t.y"},
		{"duplicate subexpr", ast.And(ast.Eq(ast.Col("t", "x"), ast.Col("t", "x")),
			ast.Eq(ast.Col("t", "x"), ast.Col("t", "x"))), "", "(= t.x t.x)"},
	}
	for _, tc := range cases {
		out, _, err := rules.Fixpoint(tc.tree, rules.Std())
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s: want error containing %q, got %v", tc.name, tc.wantErr, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
		} else if got := out.String(); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestOscillation(t *testing.T) {
	swap := func(from, to string) rules.Rule {
		return rules.Rule{Name: from + "-to-" + to, Apply: func(n *ast.Node) (*ast.Node, bool) {
			if n.Kind == ast.KColumn && n.Name == from {
				return ast.Col("t", to), true
			}
			return n, false
		}}
	}
	cases := []struct {
		name  string
		rules []rules.Rule
	}{
		{"inverse pair", []rules.Rule{swap("x", "y"), swap("y", "x")}},
	}
	for _, tc := range cases {
		_, _, err := rules.Fixpoint(ast.Col("t", "x"), tc.rules)
		if err == nil || !strings.Contains(err.Error(), "oscillation") {
			t.Errorf("%s: want oscillation error, got %v", tc.name, err)
		}
	}
}

func TestDeterminism(t *testing.T) {
	x, y := ast.Col("t", "x"), ast.Col("t", "y")
	tree := ast.Not(ast.And(ast.Or(x, ast.Const(ast.False)), ast.Not(ast.Not(y)), x))
	want, _, err := rules.Fixpoint(tree, rules.Std())
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 20; i++ {
		rs := rules.Std()
		r.Shuffle(len(rs), func(a, b int) { rs[a], rs[b] = rs[b], rs[a] })
		got, _, err := rules.Fixpoint(tree, rs)
		if err != nil {
			t.Fatal(err)
		}
		if got.String() != want.String() {
			t.Fatalf("shuffle %d: got %s, want %s", i, got, want)
		}
	}
}
