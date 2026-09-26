// Command demo prints self-check verdicts for the LR(1) closure/goto packages.
package main

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/gram"
	"ontology/lr"
)

func sy(s string) gram.Symbol { return gram.Symbol(s) }
func itemStr(it lr.Item) string {
	out := "[" + string(it.Head) + " -> "
	for i, x := range it.Body {
		if i == it.Dot {
			out += "· "
		}
		out += string(x) + " "
	}
	if it.Dot == len(it.Body) {
		out += "· "
	}
	return out[:len(out)-1] + ", " + string(it.Lookahead) + "]"
}
func show(cl []lr.Item) (s string) {
	for _, it := range cl {
		s += itemStr(it) + " "
	}
	return s
}
func ok(cond bool, name, detail string) {
	if cond {
		fmt.Println("OK", name, detail)
	} else {
		fmt.Println("FAIL", name, detail)
	}
}

func main() {
	g, _ := gram.New("S'", []gram.Symbol{"c", "d"}, []gram.Symbol{"S'", "S", "C"}, []gram.Production{
		{Head: "S'", Body: []gram.Symbol{sy("S")}},
		{Head: "S", Body: []gram.Symbol{sy("C"), sy("C")}},
		{Head: "C", Body: []gram.Symbol{sy("c"), sy("C")}},
		{Head: "C", Body: []gram.Symbol{sy("d")}},
	})
	i0 := []lr.Item{{Head: "S'", Body: []gram.Symbol{sy("S")}, Lookahead: "$"}}
	cl, _ := api.ClosureOf(g, i0)
	steps := []lr.Item{ // derivation order from NOTES.md, steps 1..6
		{Head: "S'", Body: []gram.Symbol{sy("S")}, Lookahead: "$"},
		{Head: "S", Body: []gram.Symbol{sy("C"), sy("C")}, Lookahead: "$"},
		{Head: "C", Body: []gram.Symbol{sy("c"), sy("C")}, Lookahead: "c"},
		{Head: "C", Body: []gram.Symbol{sy("c"), sy("C")}, Lookahead: "d"},
		{Head: "C", Body: []gram.Symbol{sy("d")}, Lookahead: "c"},
		{Head: "C", Body: []gram.Symbol{sy("d")}, Lookahead: "d"},
	}
	ok(len(cl) == 6 && lr.Equal(cl, steps), "closure steps 1-6 (6 items)", show(steps))
	gc, _ := api.GotoOf(g, cl, sy("C"))
	ok(len(gc) == 3, "goto(I0,C)", show(gc))
	gx, _ := api.GotoOf(g, cl, sy("c"))
	ok(len(gx) == 6, "goto(I0,c)", show(gx))
	cores := map[string]bool{}
	for _, it := range cl {
		cores[fmt.Sprintf("%s%v%d", it.Head, it.Body, it.Dot)] = true
	}
	ok(len(cores) == 4, "LR(0) core count = 4", "")
	vg, _ := gram.New("S", []gram.Symbol{"a", "b"}, []gram.Symbol{"S", "A"}, []gram.Production{
		{Head: "S", Body: []gram.Symbol{sy("A"), sy("b")}},
		{Head: "A", Body: nil},
		{Head: "A", Body: []gram.Symbol{sy("a")}},
	})
	vc, _ := api.ClosureOf(vg, []lr.Item{{Head: "S", Body: []gram.Symbol{sy("A"), sy("b")}, Lookahead: "$"}})
	la := map[gram.Symbol]bool{}
	for _, it := range vc {
		if it.Head == "A" {
			la[it.Lookahead] = true
		}
	}
	ok(reflect.DeepEqual(la, map[gram.Symbol]bool{"b": true}), "variant A->eps|a LAs both b", show(vc))
	nOK := 0
	checks := []struct {
		e    error
		want error
	}{
		{mustErr(), gram.ErrUndeclaredSymbol},
		{func() error { _, e := gram.New("x", []gram.Symbol{"x"}, []gram.Symbol{"S"}, nil); return e }(), gram.ErrInvalidStart},
		{func() error {
			_, e := api.ClosureOf(g, []lr.Item{{Head: "C", Body: []gram.Symbol{sy("d")}, Dot: 5, Lookahead: "$"}})
			return e
		}(), lr.ErrDotOutOfRange},
		{func() error {
			_, e := api.ClosureOf(g, []lr.Item{{Head: "C", Body: []gram.Symbol{sy("d")}, Lookahead: "C"}})
			return e
		}(), lr.ErrInvalidLookahead},
	}
	for _, c := range checks {
		if errors.Is(c.e, c.want) {
			nOK++
		}
	}
	ok(nOK == 4, "four distinct sentinel errors", "")
	after, e := api.ClosureOf(g, i0)
	ok(e == nil && len(after) == 6, "state unchanged after rejection", "")
	mg, mi0 := bigGrammar(2000)
	bc, _ := api.ClosureOf(mg, mi0)
	ok(len(bc) == 2001, "large-m closure items = m+1", fmt.Sprint(len(bc)))
	res := make([][]lr.Item, 16)
	var wg sync.WaitGroup
	for i := range res {
		wg.Add(1)
		go func(i int) { defer wg.Done(); res[i], _ = api.ClosureOf(mg, mi0) }(i)
	}
	wg.Wait()
	agree := true
	for i := 1; i < len(res); i++ {
		agree = agree && lr.Equal(res[0], res[i])
	}
	ok(agree, "concurrent closures identical", "")
	ok(api.SelfCheck() == nil, "SelfCheck four invariants", "")
}

func mustErr() error {
	_, e := gram.New("S", []gram.Symbol{"x"}, []gram.Symbol{"S"},
		[]gram.Production{{Head: "S", Body: []gram.Symbol{sy("z")}}})
	return e
}
func bigGrammar(m int) (*gram.Grammar, []lr.Item) {
	terms, prods := make([]gram.Symbol, m), make([]gram.Production, m)
	for i := 0; i < m; i++ {
		terms[i] = gram.Symbol(fmt.Sprintf("t%d", i))
		prods[i] = gram.Production{Head: "M", Body: []gram.Symbol{terms[i]}}
	}
	terms = append(terms, "x")
	prods = append(prods, gram.Production{Head: "S", Body: []gram.Symbol{"M"}})
	g, _ := gram.New("S", terms, []gram.Symbol{"S", "M"}, prods)
	return g, []lr.Item{{Head: "S", Body: []gram.Symbol{"M"}, Lookahead: "$"}}
}
