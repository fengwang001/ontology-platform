package fold_test

import (
	"strings"
	"testing"

	"ontology/ast"
	"ontology/equiv"
	"ontology/fold"
)

var cols3 = []string{"t.a", "t.b", "t.c"}

func p() *ast.Node { return ast.Col("t", "a") }
func q() *ast.Node { return ast.Col("t", "b") }

// TestFoldRules 表驱动：每条折叠规则断言结果形态，并在 3^3=27 种赋值下
// 用 equiv 全量验证重写前后等价（过滤位置受限的规则用过滤语义验证）。
func TestFoldRules(t *testing.T) {
	cases := []struct {
		name     string
		in       *ast.Node
		want     string
		topLevel bool // 仅过滤语义等价（U→F 类）
	}{
		{"const-eval NOT", ast.Not1(ast.K(ast.True)), "FALSE", false},
		{"const-eval EQ NULL", ast.Eq2(ast.K(ast.True), ast.K(ast.Unknown)), "NULL", false},
		{"const-eval AND all NULL", ast.AndN(ast.K(ast.Unknown), ast.K(ast.Unknown)), "NULL", false},
		{"and-annihilator", ast.AndN(p(), ast.K(ast.False)), "FALSE", false},
		{"or-annihilator", ast.OrN(p(), ast.K(ast.True)), "TRUE", false},
		{"and-identity", ast.AndN(p(), ast.K(ast.True)), "t.a", false},
		{"or-identity", ast.OrN(p(), ast.K(ast.False)), "t.a", false},
		{"and-not-a at top", ast.AndN(p(), ast.Not1(p())), "FALSE", true},
		{"single constant", ast.K(ast.True), "TRUE", false},
	}
	for _, c := range cases {
		out := fold.Fold(c.in)
		if out.String() != c.want {
			t.Fatalf("%s: got %s want %s", c.name, out, c.want)
		}
		if c.topLevel {
			if bad := equiv.CheckTop(c.in, out, cols3); bad != nil {
				t.Fatalf("%s: not filter-equivalent at %s", c.name, bad)
			}
			continue
		}
		if bad := equiv.Check(c.in, out, cols3); bad != nil {
			t.Fatalf("%s: not equivalent at %s", c.name, bad)
		}
	}
}

// TestNonRules 三条三值逻辑下不成立的重写必须不被应用。
func TestNonRules(t *testing.T) {
	x := ast.Col("t", "x")
	cases := []struct {
		name string
		in   *ast.Node
		want ast.Kind
	}{
		{"x=x not folded", ast.Eq2(x, x), ast.Eq},
		{"A OR NOT A not folded", ast.OrN(p(), ast.Not1(p())), ast.Or},
		{"A AND NOT A kept under NOT", ast.Not1(ast.AndN(p(), ast.Not1(p()))), ast.Not},
		{"A AND NOT A kept inside EQ", ast.Eq2(ast.AndN(p(), ast.Not1(p())), q()), ast.Eq},
	}
	for _, c := range cases {
		if got := fold.Fold(c.in); got.Kind != c.want {
			t.Fatalf("%s: got %s", c.name, got)
		}
	}
}

// TestPositionSensitivity 同一子表达式：顶层过滤位置折叠，NOT 之下保持。
func TestPositionSensitivity(t *testing.T) {
	s := func() *ast.Node { return ast.AndN(p(), ast.Not1(p())) }
	cases := []struct {
		name string
		in   *ast.Node
		want string
	}{
		{"top level", s(), "FALSE"},
		{"under AND at top", ast.AndN(s(), q()), "FALSE"},
		{"under OR at top", ast.OrN(s(), q()), "t.b"},
		{"under NOT", ast.Not1(s()), "NOT(AND(NOT(t.a), t.a))"},
		{"under NOT under AND", ast.AndN(ast.Not1(s()), q()), "AND(NOT(AND(NOT(t.a), t.a)), t.b)"},
	}
	for _, c := range cases {
		if got := fold.Fold(c.in).String(); got != c.want {
			t.Fatalf("%s: got %s want %s", c.name, got, c.want)
		}
	}
}

// TestFilterCollapseSemantics A AND NOT A→FALSE：3VL 下不等价（A=U 时 U≠F），
// 过滤语义下等价；差异必须精确出现在 A=U 这组赋值上。
func TestFilterCollapseSemantics(t *testing.T) {
	in := ast.AndN(p(), ast.Not1(p()))
	out := ast.K(ast.False)
	bad := equiv.Check(in, out, cols3)
	if bad == nil {
		t.Fatal("raw 3VL equivalence should fail")
	}
	if bad["t.a"] != ast.Unknown {
		t.Fatalf("differing assignment should have t.a=U, got %s", bad)
	}
	if got := equiv.CheckTop(in, out, cols3); got != nil {
		t.Fatalf("filter equivalence should hold, differ at %s", got)
	}
}

// TestDiffReportsAssignment 故意验证一条错误规则，断言错误指出具体赋值。
func TestDiffReportsAssignment(t *testing.T) {
	err := equiv.Diff(ast.Eq2(ast.Col("t", "x"), ast.Col("t", "x")), ast.K(ast.True), nil)
	if err == nil || !strings.Contains(err.Error(), "t.x=U") {
		t.Fatalf("want assignment t.x=U in error, got %v", err)
	}
}
