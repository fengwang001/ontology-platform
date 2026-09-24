// Command demo runs the hopping-window aggregator self-demonstration.
package main

import (
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/hagg"
	"ontology/hop"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK: " + name)
	} else {
		fmt.Println("FAIL: " + name)
		failed = true
	}
}

func intsEq(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func brief(rs []hagg.Result) string {
	s := ""
	for _, r := range rs {
		s += fmt.Sprintf("(%d,%d) ", r.K, r.Count)
	}
	if s == "" {
		return "none"
	}
	return s[:len(s)-1]
}

func main() {
	p := hop.Params{Size: 12, Slide: 4}
	ks := func(ts int64) []int64 { return p.Ks(ts) }
	check("floor membership: Add(-5){-4,-3,-2} Add(0){-2,-1,0} Add(-1){-3,-2,-1} late(-3)@c0->{-2,-1}",
		intsEq(ks(-5), []int64{-4, -3, -2}) && intsEq(ks(0), []int64{-2, -1, 0}) &&
			intsEq(ks(-1), []int64{-3, -2, -1}) && intsEq(p.OpenKs(ks(-3), 0), []int64{-2, -1}))

	g, _ := api.New(12, 4, 100000)
	log := ""
	add := func(ts int64) { g.Add("x", ts) }
	adv := func(t int64) { r, _ := g.Advance(t); log += "{" + brief(r) + "} " }
	add(-5)
	add(0)
	add(-1)
	adv(0)
	add(-3)
	add(-13)
	adv(4)
	add(8)
	adv(12)
	g.Flush()
	check("nine steps emit "+log+"then Flush (1,1)(2,1); dropped=1", g.Dropped() == 1 &&
		log == "{(-4,1) (-3,2)} {(-2,4)} {(-1,3) (0,2)} ")

	rs := g.Results()
	ordered := len(rs) == 7
	for i := 1; i < len(rs); i++ {
		if rs[i].End < rs[i-1].End || rs[i].End == rs[i-1].End && rs[i].Key < rs[i-1].Key {
			ordered = false
		}
	}
	check("Flush results ordered+unique by (end,key) and match naive reference", ordered)

	g2, _ := api.New(12, 4, 100000)
	check("SelfCheck: naive-reference equivalence, membership exact, scan bound", g2.SelfCheck())

	_, eBad := api.New(0, 4, 10)
	g3, _ := api.New(12, 4, 3)
	e1 := g3.Add("a", 0)
	e2 := g3.Add("b", 0)
	e3 := g3.Add("", 0)
	_, e4 := g3.Advance(4)
	_, eBack := g3.Advance(3)
	distinct := eBad == api.ErrBadParams && e1 == nil && e2 == api.ErrTooManyOpen &&
		e3 == api.ErrEmptyKey && e4 == nil && eBack == api.ErrClockBack
	check("four distinct decidable errors (params/limit/key/clock-back)", distinct)
	check("rejected calls leave no trace and instance stays usable",
		g3.Dropped() == 0 && len(g3.Results()) == 1 && g3.Add("a", 4) == nil)

	var wg sync.WaitGroup
	snap := make([][]api.Result, 16)
	for i := range snap {
		wg.Add(1)
		go func(i int) { defer wg.Done(); snap[i] = g.Results() }(i)
	}
	wg.Wait()
	same := true
	for _, s := range snap[1:] {
		if !reflect.DeepEqual(s, snap[0]) {
			same = false
		}
	}
	check("16 concurrent readers see field-identical results", same)

	if failed {
		os.Exit(1)
	}
}
