package ast_test

import (
	"errors"
	"strings"
	"testing"

	"ontology/ast"
)

func TestEvalTruthTables(t *testing.T) {
	vals := []ast.Tri{ast.False, ast.Unknown, ast.True}
	andWant := [3][3]ast.Tri{
		{ast.False, ast.False, ast.False},
		{ast.False, ast.Unknown, ast.Unknown},
		{ast.False, ast.Unknown, ast.True},
	}
	orWant := [3][3]ast.Tri{
		{ast.False, ast.Unknown, ast.True},
		{ast.Unknown, ast.Unknown, ast.True},
		{ast.True, ast.True, ast.True},
	}
	for i, a := range vals {
		if ast.Not3(a) != ast.True-a && a != ast.Unknown {
			t.Fatalf("Not3(%d)", a)
		}
		if ast.Not3(ast.Unknown) != ast.Unknown {
			t.Fatal("Not3(U) != U")
		}
		for j, b := range vals {
			env := map[string]ast.Tri{"t.a": a, "t.b": b}
			if got := ast.AndN(ast.Col("t", "a"), ast.Col("t", "b")).Eval(env); got != andWant[i][j] {
				t.Fatalf("AND(%d,%d)=%d want %d", a, b, got, andWant[i][j])
			}
			if got := ast.OrN(ast.Col("t", "a"), ast.Col("t", "b")).Eval(env); got != orWant[i][j] {
				t.Fatalf("OR(%d,%d)=%d want %d", a, b, got, orWant[i][j])
			}
		}
	}
}

func TestStringCanonical(t *testing.T) {
	cases := []struct {
		in   *ast.Node
		want string
	}{
		{ast.K(ast.True), "TRUE"},
		{ast.K(ast.Unknown), "NULL"},
		{ast.Col("s", "c1"), "s.c1"},
		{ast.Not1(ast.Col("s", "c1")), "NOT(s.c1)"},
		{ast.AndN(ast.Col("t", "b"), ast.Col("t", "a")), "AND(t.a, t.b)"},
		{ast.OrN(ast.Col("t", "b"), ast.K(ast.False)), "OR(FALSE, t.b)"},
		{ast.Eq2(ast.Col("t", "b"), ast.Col("t", "a")), "EQ(t.a, t.b)"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Fatalf("String()=%q want %q", got, c.want)
		}
	}
}

func TestCheckErrors(t *testing.T) {
	cases := []struct {
		name string
		in   *ast.Node
		want error
	}{
		{"empty tree", nil, ast.ErrEmpty},
		{"bad kind", &ast.Node{}, nil},
		{"empty column", ast.Col("", "c"), nil},
		{"not with 2 kids", &ast.Node{Kind: ast.Not, Kids: []*ast.Node{ast.K(ast.True), ast.K(ast.True)}}, nil},
		{"and with 1 kid", &ast.Node{Kind: ast.And, Kids: []*ast.Node{ast.K(ast.True)}}, nil},
		{"nested bad", ast.Not1(&ast.Node{}), nil},
	}
	for _, c := range cases {
		err := ast.Check(c.in)
		if c.want != nil {
			if !errors.Is(err, c.want) {
				t.Fatalf("%s: err=%v want %v", c.name, err, c.want)
			}
		} else if err == nil {
			t.Fatalf("%s: want error, got nil", c.name)
		}
	}
	if err := ast.Check(ast.Col("t", "a")); err != nil {
		t.Fatalf("valid tree rejected: %v", err)
	}
}

func TestDeepChainNoOverflow(t *testing.T) {
	n := ast.Col("t", "a")
	for i := 0; i < 1000; i++ {
		n = ast.Not1(n)
	}
	if n.Size() != 1001 {
		t.Fatalf("size=%d", n.Size())
	}
	if got := n.Eval(map[string]ast.Tri{"t.a": ast.True}); got != ast.True {
		t.Fatalf("eval=%d", got)
	}
	if !strings.HasPrefix(n.String(), "NOT(") {
		t.Fatal("print broken")
	}
}

func TestDegenerateAndDuplicate(t *testing.T) {
	p := ast.Col("t", "a")
	if ast.AndN(p) != p || ast.OrN(p) != p {
		t.Fatal("single-child AND/OR should collapse to the child")
	}
	if ast.AndN().String() != "TRUE" || ast.OrN().String() != "FALSE" {
		t.Fatal("zero-child AND/OR should be identity constants")
	}
	dup := ast.AndN(p, p, ast.Not1(p))
	if got := dup.Eval(map[string]ast.Tri{"t.a": ast.True}); got != ast.False {
		t.Fatalf("duplicate subexpr eval=%d", got)
	}
	if dup.Size() != 5 {
		t.Fatalf("no CSE expected, size=%d", dup.Size())
	}
	allNull := ast.AndN(ast.K(ast.Unknown), ast.OrN(ast.K(ast.Unknown)))
	if got := allNull.Eval(nil); got != ast.Unknown {
		t.Fatalf("all-NULL eval=%d", got)
	}
}
