package fold_test

import (
	"testing"

	"ontology/ast"
	"ontology/equiv"
	"ontology/rules"
)

func c(name string) *ast.Node { return ast.Col("t", name) }

func rewrite(t *testing.T, n *ast.Node) string {
	t.Helper()
	out, _, err := rules.Fixpoint(n, rules.Std())
	if err != nil {
		t.Fatalf("fixpoint: %v", err)
	}
	return out.String()
}

func TestFoldCases(t *testing.T) {
	a, b, x := c("a"), c("b"), c("x")
	cases := []struct {
		name string
		in   *ast.Node
		want string
	}{
		{"const-eval-and", ast.And(ast.Const(ast.True), ast.Const(ast.False)), "false"},
		{"const-eval-or", ast.Or(ast.Const(ast.False), ast.Const(ast.Unknown)), "unk"},
		{"const-eval-not", ast.Not(ast.Const(ast.True)), "false"},
		{"const-eval-cmp", ast.Eq(ast.Const(ast.True), ast.Const(ast.False)), "false"},
		{"and-true", ast.And(x, ast.Const(ast.True)), "t.x"},
		{"and-false", ast.And(x, ast.Const(ast.False)), "false"},
		{"or-false", ast.Or(x, ast.Const(ast.False)), "t.x"},
		{"or-true", ast.Or(x, ast.Const(ast.True)), "true"},
		{"not-not", ast.Not(ast.Not(x)), "t.x"},
		{"de-morgan-and", ast.Not(ast.And(a, b)), "(or (not t.a) (not t.b))"},
		{"de-morgan-or", ast.Not(ast.Or(a, b)), "(and (not t.a) (not t.b))"},
		{"single-kid", ast.And(x), "t.x"},
		{"dup-kid", ast.And(ast.Eq(x, x), ast.Eq(x, x)), "(= t.x t.x)"},
		{"x=x kept", ast.Eq(x, x), "(= t.x t.x)"},
		{"excluded-middle kept", ast.Or(a, ast.Not(a)), "(or (not t.a) t.a)"},
		{"contradiction at top", ast.And(x, ast.Not(x)), "false"},
		{"contradiction nested in top AND", ast.And(a, ast.And(x, ast.Not(x))), "false"},
		{"contradiction under NOT kept", ast.Not(ast.And(x, ast.Not(x))), "(or (not t.x) t.x)"},
		{"contradiction under OR kept", ast.Or(a, ast.And(x, ast.Not(x))), "(or (and (not t.x) t.x) t.a)"},
	}
	for _, tc := range cases {
		if got := rewrite(t, tc.in); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestRuleEquivalence(t *testing.T) {
	a, b, d, e := c("a"), c("b"), c("d"), c("e")
	tr, fl, un := ast.Const(ast.True), ast.Const(ast.False), ast.Const(ast.Unknown)
	cases := []struct {
		rule          string
		before, after *ast.Node
		exact         bool
	}{
		{"and-true", ast.And(a, tr, b, d), ast.And(a, b, d), true},
		{"and-false", ast.And(a, b, d, fl), fl, true},
		{"or-false", ast.Or(a, b, d, fl), ast.Or(a, b, d), true},
		{"or-true", ast.Or(a, b, d, tr), tr, true},
		{"not-not", ast.Not(ast.Not(ast.And(a, ast.Or(b, d)))), ast.And(a, ast.Or(b, d)), true},
		{"de-morgan-and", ast.Not(ast.And(a, b, d)), ast.Or(ast.Not(a), ast.Not(b), ast.Not(d)), true},
		{"de-morgan-or", ast.Not(ast.Or(a, b, d, e)), ast.And(ast.Not(a), ast.Not(b), ast.Not(d), ast.Not(e)), true},
		{"single-kid", ast.And(ast.Or(a, ast.Ne(b, d))), ast.Or(a, ast.Ne(b, d)), true},
		{"dup-kid", ast.And(ast.Eq(a, b), ast.Or(d, e), ast.Eq(a, b)), ast.And(ast.Eq(a, b), ast.Or(d, e)), true},
		{"const-eval", ast.And(tr, un, ast.Or(fl, tr)), un, true},
		{"contradiction", ast.And(a, ast.Or(b, d), ast.Not(a)), fl, false},
	}
	for _, tc := range cases {
		eq, ce := equiv.Equal(tc.before, tc.after)
		feq, fce := equiv.FilterEqual(tc.before, tc.after)
		if tc.exact && !eq {
			t.Errorf("rule %s NOT equivalent at %s", tc.rule, ce)
		}
		if !tc.exact && eq {
			t.Errorf("rule %s unexpectedly exact; want filter-only", tc.rule)
		}
		if !feq {
			t.Errorf("rule %s not even filter-equivalent at %s", tc.rule, fce)
		}
	}
}

func TestThreeValuedSemantics(t *testing.T) {
	x := c("x")
	unk := map[string]ast.Value{"t.x": ast.Unknown}
	cases := []struct {
		name string
		expr *ast.Node
		env  map[string]ast.Value
		want ast.Value
	}{
		{"x=x is unknown when x NULL", ast.Eq(x, x), unk, ast.Unknown},
		{"A OR NOT A is unknown when A unknown", ast.Or(x, ast.Not(x)), unk, ast.Unknown},
		{"A AND NOT A is unknown when A unknown", ast.And(x, ast.Not(x)), unk, ast.Unknown},
		{"de-morgan holds at (unk,false)", ast.Not(ast.And(x, ast.Const(ast.False))), unk, ast.True},
		{"NOT of contradiction is unknown, not true", ast.Not(ast.And(x, ast.Not(x))), unk, ast.Unknown},
	}
	for _, tc := range cases {
		if got := tc.expr.Eval(tc.env); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}
