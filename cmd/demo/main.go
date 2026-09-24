package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sync"

	"ontology/api"
)

type tab = map[int64]map[string]int
type jk struct {
	k    int64
	a, b string
}

func njoin(r, s tab) map[jk]int {
	j := map[jk]int{}
	for k, rm := range r {
		for a, mr := range rm {
			for b, ms := range s[k] {
				j[jk{k, a, b}] += mr * ms
			}
		}
	}
	return j
}
func napply(t tab, rows []api.Row) {
	for _, x := range rows {
		if t[x.K] == nil {
			t[x.K] = map[string]int{}
		}
		t[x.K][x.V] += x.Sign
		if t[x.K][x.V] == 0 {
			delete(t[x.K], x.V)
		}
	}
}
func vm(ds []api.Delta) map[jk]int {
	m := map[jk]int{}
	for _, d := range ds {
		m[jk{d.K, d.A, d.B}] = d.Mult
	}
	return m
}
func genBatch(rng *rand.Rand, live tab) []api.Row {
	out, pend := []api.Row{}, tab{}
	for i := 0; i < 3; i++ {
		k := int64(rng.Intn(8))
		v := string(rune('a' + rng.Intn(3)))
		sign := 1
		if rng.Intn(3) == 0 && live[k][v]+pend[k][v] > 0 {
			sign = -1
		}
		if pend[k] == nil {
			pend[k] = map[string]int{}
		}
		pend[k][v] += sign
		out = append(out, api.Row{K: k, V: v, Sign: sign})
	}
	return out
}
func main() {
	ok := true
	chk := func(name string, good bool) {
		fmt.Println(map[bool]string{true: "OK  ", false: "FAIL "}[good] + name)
		ok = ok && good
	}
	j := api.New(100) // section 3: exact per-batch deltas; views match recompute
	dr := [][]api.Row{{{K: 1, V: "x", Sign: 1}, {K: 2, V: "y", Sign: 1}}, {{K: 1, V: "z", Sign: 1}, {K: 2, V: "y", Sign: -1}}, {{K: 1, V: "x", Sign: -1}}}
	ds := [][]api.Row{{{K: 1, V: "p", Sign: 1}, {K: 2, V: "q", Sign: 1}}, {{K: 1, V: "r", Sign: 1}, {K: 2, V: "q", Sign: 1}}, {{K: 1, V: "r", Sign: -1}}}
	want := [][]api.Delta{
		{{K: 1, A: "x", B: "p", Mult: 1}, {K: 2, A: "y", B: "q", Mult: 1}}, {{K: 1, A: "x", B: "r", Mult: 1}, {K: 1, A: "z", B: "p", Mult: 1}, {K: 1, A: "z", B: "r", Mult: 1}, {K: 2, A: "y", B: "q", Mult: -1}},
		{{K: 1, A: "x", B: "p", Mult: -1}, {K: 1, A: "x", B: "r", Mult: -1}, {K: 1, A: "z", B: "r", Mult: -1}},
	}
	nr, ns, g1 := tab{}, tab{}, true
	for i := range dr {
		out, err := j.Feed(dr[i], ds[i])
		napply(nr, dr[i])
		napply(ns, ds[i])
		g1 = g1 && err == nil && reflect.DeepEqual(out, want[i]) && reflect.DeepEqual(vm(j.View()), njoin(nr, ns))
	}
	chk("section3 deltas+views", g1)
	j2, rng := api.New(1<<20), rand.New(rand.NewSource(7)) // random batches
	nr2, ns2, down := tab{}, tab{}, map[jk]int{}
	g2, g3, g4 := true, true, true
	for it := 0; it < 200; it++ {
		dR, dS := genBatch(rng, nr2), genBatch(rng, ns2)
		old := njoin(nr2, ns2)
		out, err := j2.Feed(dR, dS)
		if err != nil {
			g2 = false
			break
		}
		napply(nr2, dR)
		napply(ns2, dS)
		for _, d := range out {
			g := jk{d.K, d.A, d.B}
			old[g] += d.Mult
			if old[g] == 0 {
				delete(old, g)
			}
			down[g] += d.Mult
			g4 = g4 && down[g] >= 0
			if down[g] == 0 {
				delete(down, g)
			}
		}
		g2 = g2 && reflect.DeepEqual(old, njoin(nr2, ns2))
		g3 = g3 && reflect.DeepEqual(vm(j2.View()), njoin(nr2, ns2))
	}
	chk("random delta==full-diff", g2)
	chk("view==full-recompute", g3)
	chk("non-negative multiplicities", g4)
	j3 := api.New(2) // three distinct errors; rejected batch leaves state intact
	j3.Feed([]api.Row{{K: 1, V: "a", Sign: 1}}, []api.Row{{K: 1, V: "p", Sign: 1}})
	before := j3.View()
	g5 := true
	bad := func(dR, dS []api.Row, want error) {
		_, err := j3.Feed(dR, dS)
		g5 = g5 && errors.Is(err, want)
	}
	bad([]api.Row{{K: 2, V: "b", Sign: 0}}, nil, api.ErrInvalidChange)
	bad([]api.Row{{K: 2, Sign: 1}}, nil, api.ErrInvalidChange)
	bad([]api.Row{{K: 1, V: "a", Sign: -1}, {K: 1, V: "a", Sign: -1}}, nil, api.ErrDeleteMissing)
	bad([]api.Row{{K: 2, V: "x", Sign: 1}, {K: 3, V: "y", Sign: 1}}, []api.Row{{K: 2, V: "p", Sign: 1}, {K: 3, V: "q", Sign: 1}}, api.ErrViewLimit)
	chk("three distinct errors", g5)
	chk("rejection leaves state intact", reflect.DeepEqual(j3.View(), before))
	chk("probe count stable as m grows", api.New(1).SelfCheck() == nil)
	j4 := api.New(1 << 20) // concurrent disjoint-K feeds; final view == recompute
	var wg sync.WaitGroup
	nr4, ns4 := tab{}, tab{}
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(k int64) {
			defer wg.Done()
			j4.Feed([]api.Row{{K: k, V: "a", Sign: 1}}, []api.Row{{K: k, V: "b", Sign: 1}})
		}(int64(g))
		nr4[int64(g)] = map[string]int{"a": 1}
		ns4[int64(g)] = map[string]int{"b": 1}
	}
	wg.Wait()
	chk("concurrent feeds", reflect.DeepEqual(vm(j4.View()), njoin(nr4, ns4)))
	if !ok {
		os.Exit(1)
	}
}
