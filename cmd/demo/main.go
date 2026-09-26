// Command demo 逐条核验 LR(1) 闭包与 goto 的推导结果，全部 OK 时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/gram"
	"ontology/lr"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL " + name)
		return
	}
	fmt.Println("OK   " + name)
}

func it(lhs string, dot int, look string, rhs ...string) lr.Item {
	r := make([]gram.Symbol, len(rhs))
	for i, s := range rhs {
		r[i] = gram.Symbol(s)
	}
	return lr.Item{LHS: gram.Symbol(lhs), RHS: r, Dot: dot, Look: gram.Symbol(look)}
}

func spec() *gram.Grammar {
	g, err := gram.New("S'", []gram.Symbol{"c", "d"}, []gram.Symbol{"S'", "S", "C"}, []gram.Production{
		{LHS: "S'", RHS: []gram.Symbol{"S"}},
		{LHS: "S", RHS: []gram.Symbol{"C", "C"}},
		{LHS: "C", RHS: []gram.Symbol{"c", "C"}},
		{LHS: "C", RHS: []gram.Symbol{"d"}},
	})
	if err != nil {
		panic(err)
	}
	return g
}

func main() {
	g := spec()
	I0 := []lr.Item{it("S'", 0, "$", "S")}
	want := []lr.Item{ // 闭包分步表：种子 1 项 + 5 步加入
		it("S'", 0, "$", "S"), it("S", 0, "$", "C", "C"),
		it("C", 0, "c", "c", "C"), it("C", 0, "d", "c", "C"),
		it("C", 0, "c", "d"), it("C", 0, "d", "d"),
	}
	cl, err := api.ClosureOf(g, I0)
	check("closure steps: seed + 5 adds", err == nil && lr.Equal(cl, want))
	check("closure(I0) = 6 items", len(cl) == 6)
	gc, _ := api.GotoOf(g, cl, "C")
	gdot, _ := api.GotoOf(g, cl, "c")
	wantGC := []lr.Item{it("S", 1, "$", "C", "C"), it("C", 0, "$", "c", "C"), it("C", 0, "$", "d")}
	wantGd := []lr.Item{it("C", 1, "c", "c", "C"), it("C", 1, "d", "c", "C"),
		it("C", 0, "c", "c", "C"), it("C", 0, "d", "c", "C"), it("C", 0, "c", "d"), it("C", 0, "d", "d")}
	check("goto(I0,C)=3 & goto(I0,c)=6", lr.Equal(gc, wantGC) && lr.Equal(gdot, wantGd))
	cores := map[string]bool{}
	for _, x := range cl {
		cores[fmt.Sprintf("%v%v%d", x.LHS, x.RHS, x.Dot)] = true
	}
	check("LR(0) cores = 4", len(cores) == 4)
	ge, _ := gram.New("S", []gram.Symbol{"a", "b"}, []gram.Symbol{"S", "A"}, []gram.Production{
		{LHS: "S", RHS: []gram.Symbol{"A", "b"}}, {LHS: "A"}, {LHS: "A", RHS: []gram.Symbol{"a"}},
	})
	ce, _ := api.ClosureOf(ge, []lr.Item{it("S", 0, "$", "A", "b")})
	check("eps variant lookaheads = b", lr.Equal(ce, []lr.Item{
		it("S", 0, "$", "A", "b"), it("A", 0, "b"), it("A", 0, "b", "a")}))
	_, e1 := gram.New("S", []gram.Symbol{"a"}, []gram.Symbol{"S"}, []gram.Production{{LHS: "S", RHS: []gram.Symbol{"x"}}})
	_, e2 := gram.New("a", []gram.Symbol{"a"}, []gram.Symbol{"S"}, nil)
	_, e3 := api.ClosureOf(g, []lr.Item{it("S'", 2, "$", "S")})
	_, e4 := api.ClosureOf(g, []lr.Item{it("S'", 0, "S", "S")})
	sents := []error{gram.ErrUndeclaredSymbol, gram.ErrStartNotNonTerm, lr.ErrDotOutOfRange, lr.ErrBadLookahead}
	distinct := true
	for i := range sents {
		for j := i + 1; j < 4; j++ {
			distinct = distinct && sents[i] != sents[j]
		}
	}
	check("4 distinct sentinel errors", distinct && errors.Is(e1, sents[0]) &&
		errors.Is(e2, sents[1]) && errors.Is(e3, sents[2]) && errors.Is(e4, sents[3]))
	cl2, err2 := api.ClosureOf(g, I0)
	check("state unchanged after rejection", err2 == nil && lr.Equal(cl2, want))
	const m = 5000
	prods := []gram.Production{{LHS: "S", RHS: []gram.Symbol{"B"}}}
	terms := []gram.Symbol{}
	for i := 0; i < m; i++ {
		t := gram.Symbol(fmt.Sprintf("t%d", i))
		terms = append(terms, t)
		prods = append(prods, gram.Production{LHS: "B", RHS: []gram.Symbol{t}})
	}
	gm, _ := gram.New("S", terms, []gram.Symbol{"S", "B"}, prods)
	big, _ := api.ClosureOf(gm, []lr.Item{it("S", 0, "$", "B")})
	check("large-m closure m=5000 once-each", len(big) == m+1)
	var bad atomic.Bool
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				got, err := api.ClosureOf(g, I0)
				if err != nil || !lr.Equal(got, want) {
					bad.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	check("concurrent closure consistent", !bad.Load())
	check("SelfCheck", api.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
