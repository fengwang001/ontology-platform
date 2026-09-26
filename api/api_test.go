package api

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/gram"
	"ontology/lr"
)

func sym(p string, i int) gram.Symbol { return gram.Symbol(fmt.Sprintf("%s%d", p, i)) }

// randomGrammar builds random CFGs with empty bodies and nullable chains.
func randomGrammar(r *rand.Rand) (*gram.Grammar, []gram.Production, []gram.Symbol) {
	nT, nN := 1+r.Intn(3), 2+r.Intn(4)
	terms, non := make([]gram.Symbol, nT), make([]gram.Symbol, nN)
	for i := 0; i < nN; i++ {
		if i < nT {
			terms[i] = sym("t", i)
		}
		non[i] = sym("N", i)
	}
	var prods []gram.Production
	for _, h := range non {
		for k := 1 + r.Intn(2); k > 0; k-- {
			var body []gram.Symbol // may be empty (epsilon)
			for l := r.Intn(3); l > 0; l-- {
				if r.Intn(2) == 0 {
					body = append(body, terms[r.Intn(nT)])
				} else {
					body = append(body, non[r.Intn(nN)])
				}
			}
			prods = append(prods, gram.Production{Head: h, Body: body})
		}
	}
	g, err := gram.New(non[0], terms, non, prods)
	if err != nil {
		panic(err)
	}
	return g, prods, terms
}
func randomSeed(r *rand.Rand, ps []gram.Production, ts []gram.Symbol) []lr.Item {
	p := ps[r.Intn(len(ps))]
	la := append(ts, gram.EOI)
	return []lr.Item{{Head: p.Head, Body: p.Body, Dot: r.Intn(len(p.Body) + 1), Lookahead: la[r.Intn(len(la))]}}
}
func TestNaiveReferenceRandom(t *testing.T) {
	for trial := 0; trial < 200; trial++ {
		r := rand.New(rand.NewSource(int64(trial) + 1))
		g, ps, ts := randomGrammar(r)
		seed := randomSeed(r, ps, ts)
		got, _ := ClosureOf(g, seed) // random seed is always valid
		if want := naiveClosure(g, seed); !lr.Equal(got, want) {
			t.Fatalf("trial %d:\n got %v\nwant %v", trial, got, want)
		}
	}
}
func TestIdempotenceAndJustification(t *testing.T) {
	g, seed := classicGrammar(), i0()
	cl, _ := ClosureOf(g, seed) // valid grammar/seed cannot error
	if again, _ := ClosureOf(g, cl); !lr.Equal(cl, again) {
		t.Fatalf("not idempotent: %v", again)
	}
	if err := justified(g, seed, cl); err != nil {
		t.Fatal(err)
	}
}
func TestGotoKernel(t *testing.T) {
	g := classicGrammar()
	cl, _ := ClosureOf(g, i0())
	for _, X := range []gram.Symbol{"C", "c", "d", "S"} {
		got, _ := GotoOf(g, cl, X)
		var kernel []lr.Item
		for _, it := range cl {
			if it.Dot < len(it.Body) && it.Body[it.Dot] == X {
				adv := lr.Item{Head: it.Head, Body: it.Body, Dot: it.Dot + 1, Lookahead: it.Lookahead}
				if adv.Body[adv.Dot-1] != X {
					t.Fatalf("kernel not over %s: %v", X, adv)
				}
				kernel = append(kernel, adv)
			}
		}
		if want, _ := ClosureOf(g, kernel); !lr.Equal(got, want) {
			t.Fatalf("X=%s goto != closure(kernel): %v", X, got)
		}
	}
}
func TestSentinelErrors(t *testing.T) {
	if _, e := gram.New("S", []gram.Symbol{"x"}, []gram.Symbol{"S"},
		[]gram.Production{{Head: "S", Body: []gram.Symbol{"z"}}}); !errors.Is(e, gram.ErrUndeclaredSymbol) {
		t.Fatal("undeclared symbol not rejected")
	}
	if _, e := gram.New("x", []gram.Symbol{"x"}, []gram.Symbol{"S"}, nil); !errors.Is(e, gram.ErrInvalidStart) {
		t.Fatal("bad start not rejected")
	}
	g := classicGrammar()
	for _, c := range []struct {
		it   lr.Item
		want error
	}{
		{lr.Item{Head: "C", Body: []gram.Symbol{"d"}, Dot: 9, Lookahead: "$"}, lr.ErrDotOutOfRange},
		{lr.Item{Head: "C", Body: []gram.Symbol{"d"}, Lookahead: "N"}, lr.ErrInvalidLookahead},
	} {
		if out, e := ClosureOf(g, []lr.Item{c.it}); !errors.Is(e, c.want) || out != nil {
			t.Fatalf("%v: e=%v out=%v", c.want, e, out)
		}
	}
	seen := map[string]bool{}
	for _, e := range []error{gram.ErrUndeclaredSymbol, gram.ErrInvalidStart, lr.ErrDotOutOfRange, lr.ErrInvalidLookahead} {
		if seen[e.Error()] {
			t.Fatal("sentinel errors not distinct")
		}
		seen[e.Error()] = true
	}
}
func TestFailureLeavesNoTrace(t *testing.T) {
	g := classicGrammar()
	for _, b := range [][]lr.Item{
		{{Head: "C", Body: []gram.Symbol{"d"}, Dot: 9, Lookahead: "$"}},
		{{Head: "C", Body: []gram.Symbol{"d"}, Lookahead: "N"}},
	} {
		if out, err := ClosureOf(g, b); err == nil || out != nil {
			t.Fatalf("expected rejection, out=%v err=%v", out, err)
		}
	}
	if good, err := ClosureOf(g, i0()); err != nil || len(good) != 6 {
		t.Fatalf("state changed after rejection: %d items, %v", len(good), err)
	}
}
func TestConcurrentClosure(t *testing.T) {
	g, ps, _ := randomGrammar(rand.New(rand.NewSource(7)))
	seed := []lr.Item{{Head: ps[0].Head, Body: ps[0].Body, Lookahead: "$"}}
	const N = 64
	res := make([][]lr.Item, N)
	var wg sync.WaitGroup
	for i := range res {
		wg.Go(func() { res[i], _ = ClosureOf(g, seed) })
	}
	wg.Wait()
	for i := 1; i < N; i++ {
		if !lr.Equal(res[0], res[i]) {
			t.Fatalf("goroutine %d differs", i)
		}
	}
}
