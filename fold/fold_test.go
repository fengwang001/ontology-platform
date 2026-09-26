package fold

import (
	"testing"

	"ontology/ast"
)

func TestRules(t *testing.T) {
	i, b, v := ast.Int, ast.Bool, ast.Var
	bin := ast.Bin
	div := bin("/", i(1), i(0))
	cases := []struct {
		name string
		in   *ast.Expr
		want *ast.Expr
	}{
		{"int literal", i(7), i(7)},
		{"bool literal", b(true), b(true)},
		{"var kept", v("x"), v("x")},
		{"neg folds", ast.Neg(i(3)), i(-3)},
		{"neg non-literal kept", ast.Neg(v("x")), ast.Neg(v("x"))},
		{"not folds", ast.Not(b(true)), b(false)},
		{"add", bin("+", i(2), i(3)), i(5)},
		{"div", bin("/", i(7), i(2)), i(3)},
		{"cmp", bin("<", i(2), i(3)), b(true)},
		{"bool eq", bin("==", b(true), b(false)), b(false)},
		{"type mismatch kept", bin("+", i(1), b(true)), bin("+", i(1), b(true))},
		{"unknown op kept", bin("%", i(5), i(2)), bin("%", i(5), i(2))},
		{"e+0", bin("+", v("x"), i(0)), v("x")},
		{"0+e", bin("+", i(0), v("x")), v("x")},
		{"e-0", bin("-", v("x"), i(0)), v("x")},
		{"e*1", bin("*", v("x"), i(1)), v("x")},
		{"1*e", bin("*", i(1), v("x")), v("x")},
		{"e/1", bin("/", v("x"), i(1)), v("x")},
		{"e*0", bin("*", v("x"), i(0)), i(0)},
		{"0*e", bin("*", i(0), v("x")), i(0)},
		{"true&&e", bin("&&", b(true), v("b")), v("b")},
		{"false||e", bin("||", b(false), v("b")), v("b")},
		{"if true", ast.If(b(true), i(1), i(2)), i(1)},
		{"if false", ast.If(b(false), i(1), i(2)), i(2)},
		{"if cond kept", ast.If(v("b"), bin("+", i(1), i(2)), i(0)), ast.If(v("b"), i(3), i(0))},
		{"nested", bin("*", bin("+", i(2), i(3)), i(4)), i(20)},
		{"divzero+0 keeps div", bin("+", div, i(0)), div},
	}
	for _, c := range cases {
		if got := Fold(c.in); !ast.Equal(got, c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestShortCircuit(t *testing.T) {
	i, b, v := ast.Int, ast.Bool, ast.Var
	bin := ast.Bin
	rhs := bin(">", bin("/", i(1), i(0)), i(0)) // contains a possible /0
	cases := []struct {
		name string
		in   *ast.Expr
		want *ast.Expr
	}{
		{"false&&e -> false, rhs untouched", bin("&&", b(false), rhs), b(false)},
		{"true||e -> true, rhs untouched", bin("||", b(true), rhs), b(true)},
		{"true&&e -> e folded", bin("&&", b(true), rhs), rhs}, // /0 kept, not an error
		{"non-literal left keeps rhs folded", bin("&&", v("b"), bin("+", i(1), i(2))), bin("&&", v("b"), i(3))},
	}
	for _, c := range cases {
		if got := Fold(c.in); !ast.Equal(got, c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestDivZeroKept(t *testing.T) {
	i := ast.Int
	bin := ast.Bin
	div := bin("/", i(1), i(0))
	cases := []struct {
		name string
		in   *ast.Expr
		want *ast.Expr
	}{
		{"1/0 kept", div, div},
		{"(1/0)*0 kept", bin("*", div, i(0)), bin("*", div, i(0))},
		{"0*(1/0) kept", bin("*", i(0), div), bin("*", i(0), div)},
		{"nested (1+0)/(2-2) kept", bin("/", bin("+", i(1), i(0)), bin("-", i(2), i(2))), bin("/", i(1), i(0))},
		{"if branch /0 kept", ast.If(ast.Var("b"), div, i(3)), ast.If(ast.Var("b"), div, i(3))},
	}
	for _, c := range cases {
		if got := Fold(c.in); !ast.Equal(got, c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

// TestSinglePass proves folding visits every node at most once: the
// unexported revisit counter must stay zero for any size m.
func TestSinglePass(t *testing.T) {
	var build func(m int) *ast.Expr
	build = func(m int) *ast.Expr {
		if m == 1 {
			return ast.Int(1)
		}
		return ast.Bin("+", build(m/2), build(m-m/2))
	}
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		f := &folder{seen: make(map[*ast.Expr]bool)}
		f.fold(build(m))
		if f.revisits != 0 {
			t.Fatalf("m=%d: revisits=%d, want 0", m, f.revisits)
		}
	}
}
