package main

import (
	"cmp"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"

	"ontology/api"
	"ontology/closure"
	"ontology/graph"
)

func ok(name string, cond bool) {
	if cond {
		fmt.Println("OK", name)
	} else {
		fmt.Println("FAIL", name)
	}
}

func main() {
	g := graph.New()
	ok("graph mult: add twice=2, one remove still present",
		g.Add("x", "y") && !g.Add("x", "y") && g.Multiplicity("x", "y") == 2 &&
			!g.Remove("x", "y") && g.HasEdge("x", "y") && g.Remove("x", "y") && !g.HasEdge("x", "y"))

	cl := closure.New()
	sizes := []int{1, 3, 3, 9, 9, 9, 6, 9, 5, 3}
	type dr struct{ od, rd int }
	wantDR := map[int]dr{6: {0, 0}, 7: {9, 6}, 9: {9, 5}}
	ten := []struct {
		op   byte
		u, v string
	}{
		{'a', "A", "B"}, {'a', "B", "C"}, {'a', "A", "C"}, {'a', "C", "A"},
		{'a', "B", "C"}, {'r', "B", "C"}, {'r', "A", "B"}, {'a', "C", "D"},
		{'r', "C", "A"}, {'r', "B", "C"},
	}
	good := true
	for i, o := range ten {
		if o.op == 'r' {
			od, rd, _ := cl.RemoveEdge(o.u, o.v)
			if w, ok := wantDR[i+1]; ok {
				good = good && od == w.od && rd == w.rd
			}
		} else {
			cl.AddEdge(o.u, o.v)
		}
		good = good && len(cl.Pairs()) == sizes[i]
	}
	ok("ten steps: |R| sizes and steps 6/7/9 OD/RD", good)

	eng, _ := api.New(100)
	ok("api SelfCheck: four invariants", eng.SelfCheck() == nil)
	_ = eng.AddEdge("u", "v")
	_ = eng.AddEdge("u", "v")
	_, _, _ = eng.RemoveEdge("u", "v")
	ok("repeat add then one remove keeps reachability", eng.Reachable("u", "v"))

	rng := rand.New(rand.NewPCG(1, 2))
	re, _ := api.New(100)
	ref := graph.New()
	nodes := []string{"n0", "n1", "n2", "n3", "n4"}
	rg := true
	for i := 0; i < 2000; i++ {
		u, v := nodes[rng.IntN(5)], nodes[rng.IntN(5)]
		if ref.HasEdge(u, v) && rng.IntN(2) == 0 {
			if _, _, e := re.RemoveEdge(u, v); e != nil {
				rg = false
			}
			ref.Remove(u, v)
		} else if e := re.AddEdge(u, v); e == nil {
			ref.Add(u, v)
		} else {
			rg = false
		}
		if i%37 == 0 && !slices.Equal(re.Pairs(), ref.NaivePairs()) {
			rg = false
		}
	}
	ok("2000 random add/remove match naive BFS", rg && slices.Equal(re.Pairs(), ref.NaivePairs()))

	_, e0 := api.New(0)
	bad, _ := api.New(1)
	_, _, e1 := bad.RemoveEdge("a", "b")
	_ = bad.AddEdge("a", "b")
	four := errors.Is(e0, api.ErrInvalidMaxEdges) && errors.Is(bad.AddEdge("", "z"), api.ErrEmptyNode) &&
		errors.Is(e1, api.ErrEdgeNotExist) && errors.Is(bad.AddEdge("c", "d"), api.ErrTooManyEdges)
	ok("four distinct sentinel errors; rejected state untouched", four && len(bad.Pairs()) == 1)

	bm := true
	for _, m := range []int{100, 1000, 10000} {
		e, _ := api.New(m + 10)
		base := make([][2]string, 0, m)
		for i := 0; i < m; i++ {
			p := [2]string{fmt.Sprintf("x%d", i), fmt.Sprintf("y%d", i)}
			_ = e.AddEdge(p[0], p[1])
			base = append(base, p)
		}
		_ = e.AddEdge("zz", "qq")
		od, rd, _ := e.RemoveEdge("zz", "qq")
		slices.SortFunc(base, func(a, b [2]string) int {
			return cmp.Or(cmp.Compare(a[0], b[0]), cmp.Compare(a[1], b[1]))
		})
		bm = bm && od == 1 && rd == 0 && slices.Equal(e.Pairs(), base)
	}
	ok("big m: isolated add/del touches none of the m foreign pairs", bm)

	cg, _ := api.New(1000)
	for _, p := range [][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}, {"a", "d"}} {
		_ = cg.AddEdge(p[0], p[1])
	}
	want := cg.Pairs()
	var wg sync.WaitGroup
	agree := true
	var mu sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 500; k++ {
				got := cg.Pairs()
				mu.Lock()
				agree = agree && slices.Equal(got, want)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	ok("8 concurrent readers get identical Pairs", agree)
}
