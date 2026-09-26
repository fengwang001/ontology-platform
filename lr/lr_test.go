package lr

import (
	"fmt"
	"testing"

	"ontology/gram"
)

func specGrammar(t *testing.T) *gram.Grammar {
	t.Helper()
	g, err := gram.New("S'", []gram.Symbol{"c", "d"}, []gram.Symbol{"S'", "S", "C"}, []gram.Production{
		{LHS: "S'", RHS: []gram.Symbol{"S"}},
		{LHS: "S", RHS: []gram.Symbol{"C", "C"}},
		{LHS: "C", RHS: []gram.Symbol{"c", "C"}},
		{LHS: "C", RHS: []gram.Symbol{"d"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func it(lhs string, dot int, look string, rhs ...string) Item {
	r := make([]gram.Symbol, len(rhs))
	for i, s := range rhs {
		r[i] = gram.Symbol(s)
	}
	return Item{LHS: gram.Symbol(lhs), RHS: r, Dot: dot, Look: gram.Symbol(look)}
}

// TestClosureSpec 钉住第三节推导出的闭包 6 项与幂等性。
func TestClosureSpec(t *testing.T) {
	g := specGrammar(t)
	I0 := []Item{it("S'", 0, "$", "S")}
	want := []Item{
		it("S'", 0, "$", "S"), it("S", 0, "$", "C", "C"),
		it("C", 0, "c", "c", "C"), it("C", 0, "d", "c", "C"),
		it("C", 0, "c", "d"), it("C", 0, "d", "d"),
	}
	cl := Closure(g, I0)
	if !Equal(cl, want) {
		t.Errorf("closure(I0) = %v, want %v", cl, want)
	}
	if !Equal(Closure(g, cl), cl) {
		t.Error("closure not idempotent")
	}
}

// TestGoto 钉住 goto 的项数与「· 前恰以 X 结尾」的核性质。
func TestGoto(t *testing.T) {
	g := specGrammar(t)
	cl := Closure(g, []Item{it("S'", 0, "$", "S")})
	for _, tc := range []struct {
		x string
		n int
	}{
		{"S", 1}, {"C", 3}, {"c", 6}, {"d", 2}, {"z", 0},
	} {
		got := Goto(g, cl, gram.Symbol(tc.x))
		if len(got) != tc.n {
			t.Errorf("goto(I0,%s) = %d items, want %d", tc.x, len(got), tc.n)
		}
		for _, item := range got {
			if item.Dot > 0 && item.RHS[item.Dot-1] != gram.Symbol(tc.x) {
				t.Errorf("goto(I0,%s): kernel item %v not advanced over X", tc.x, item)
			}
		}
	}
}

// TestClosureRechecks 大 m 下每个项至多被处理一次：重复检查数恒为 0。
func TestClosureRechecks(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			prods := []gram.Production{{LHS: "S", RHS: []gram.Symbol{"B"}}}
			terms := []gram.Symbol{}
			for i := 0; i < m; i++ {
				sym := gram.Symbol(fmt.Sprintf("t%d", i))
				terms = append(terms, sym)
				prods = append(prods, gram.Production{LHS: "B", RHS: []gram.Symbol{sym}})
			}
			g, err := gram.New("S", terms, []gram.Symbol{"S", "B"}, prods)
			if err != nil {
				t.Fatal(err)
			}
			got, rechecks := closureStats(g, []Item{it("S", 0, "$", "B")})
			if rechecks != 0 {
				t.Errorf("rechecks = %d, want 0 (work queue, no rescan)", rechecks)
			}
			if len(got) != m+1 {
				t.Errorf("closure size = %d, want %d", len(got), m+1)
			}
		})
	}
}

// TestValidate 钉住两类项级哨兵错误。
func TestValidate(t *testing.T) {
	g := specGrammar(t)
	for _, tc := range []struct {
		name string
		item Item
		want error
	}{
		{"dot beyond rhs", it("S'", 2, "$", "S"), ErrDotOutOfRange},
		{"negative dot", it("S'", -1, "$", "S"), ErrDotOutOfRange},
		{"nonterminal lookahead", it("S'", 0, "S", "S"), ErrBadLookahead},
		{"undeclared lookahead", it("S'", 0, "x", "S"), ErrBadLookahead},
		{"valid", it("S'", 1, "$", "S"), nil},
	} {
		if err := Validate(g, []Item{tc.item}); err != tc.want {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
}
