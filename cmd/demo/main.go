// Command demo verifies the materialized-view dependency-tracking exercise.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"

	"ontology/api"
	"ontology/dep"
)

var fail bool
var vn = []string{"V1", "V2", "V3"}
var all = []string{"B1", "B2", "B3", "V1", "V2", "V3"}

var g0 = dep.New(
	dep.Node{Name: "B1", Kind: dep.Base}, dep.Node{Name: "B2", Kind: dep.Base}, dep.Node{Name: "B3", Kind: dep.Base},
	dep.Node{Name: "V1", Kind: dep.View, Deps: []string{"B1", "B2"}},
	dep.Node{Name: "V2", Kind: dep.View, Deps: []string{"B2", "B3"}},
	dep.Node{Name: "V3", Kind: dep.View, Deps: []string{"V1", "V2"}})

func rep(n int, ok bool, d string) {
	fail = fail || !ok
	fmt.Printf("%d %s %s\n", n, map[bool]string{true: "OK", false: "FAIL"}[ok], d)
}
func reach(g *dep.Graph, root string) int {
	seen := map[string]bool{}
	for f := []string{root}; len(f) > 0; f = f[1:] {
		for _, d := range g.Dependents(f[0]) {
			if !seen[d] {
				seen[d], f = true, append(f, d)
			}
		}
	}
	return len(seen)
}
func main() {
	s := "" // 1: transitive closure (B2 reaches V3 twice, counted once)
	for _, b := range g0.TransitiveBases("V3") {
		s += b
	}
	rep(1, s == "B1B2B3", "transitive bases V3={B1,B2,B3}")
	e := api.New()
	steps := [7][9]int{
		{1, 1, 25, 30, 50, 80, 1, 1, 1},
		{3, 0, 0, 35, 50, 80, 0, 1, 1},
		{4, 0, 0, 35, 55, 80, 0, 0, 1},
		{5, 0, 0, 35, 55, 90, 0, 0, 0},
		{0, 1, 100, 35, 55, 90, 1, 0, 1},
		{3, 0, 0, 125, 55, 90, 0, 0, 1},
		{5, 0, 0, 125, 55, 180, 0, 0, 0},
	}
	ok := e.SelfCheck() == nil
	for _, t := range steps {
		var err error
		if t[1] == 1 {
			err = e.UpdateBase(all[t[0]], t[2])
		} else {
			err = e.Refresh(all[t[0]])
		}
		ok = err == nil && ok
		for j, n := range vn {
			gv, _ := e.Value(n)
			ok = ok && gv == t[3+j] && e.IsStale(n) == (t[6+j] == 1)
		}
	}
	rep(2, ok, "7-step values/staleness; SelfCheck four invariants")
	e, r := api.New(), rand.New(rand.NewSource(1)) // 3: valid == naive recompute
	ok = true
	for n := 0; n < 300; n++ {
		if r.Intn(2) == 0 {
			_ = e.UpdateBase(all[r.Intn(3)], r.Intn(100))
		} else {
			_ = e.Refresh(vn[r.Intn(3)])
		}
		b1, _ := e.Value("B1")
		b2, _ := e.Value("B2")
		b3, _ := e.Value("B3")
		want := map[string]int{"V1": b1 + b2, "V2": b2 + b3, "V3": b1 + 2*b2 + b3}
		for _, x := range vn {
			gv, _ := e.Value(x)
			ok = ok && (e.IsStale(x) || gv == want[x])
		}
	}
	rep(3, ok, "valid views == naive recompute (random 300)")
	e = api.New() // 4: dependency order enforced; reads current values
	_ = e.UpdateBase("B1", 100)
	ref := errors.Is(e.Refresh("V3"), api.ErrDepsNotReady)
	sv, _ := e.Value("V3")
	_ = e.Refresh("V1")
	_ = e.Refresh("V2")
	_ = e.Refresh("V3")
	fv, _ := e.Value("V3")
	rep(4, ref && sv == 80 && fv == 170, "order: stale dep rejected (80 kept), then V3=170")
	e = api.New() // 5: four distinct errors; rejects leave no trace
	ge := []error{e.UpdateBase("X", 1), e.UpdateBase("V1", 1), e.Refresh("B1"), e.Refresh("X")}
	we := []error{api.ErrNotFound, api.ErrUpdateView, api.ErrRefreshBase, api.ErrNotFound}
	ok = true
	for i := range ge {
		ok = ok && errors.Is(ge[i], we[i])
	}
	distinct := api.ErrNotFound != api.ErrUpdateView && api.ErrUpdateView != api.ErrRefreshBase &&
		api.ErrRefreshBase != api.ErrDepsNotReady && api.ErrNotFound != api.ErrDepsNotReady
	b1, _ := e.Value("B1")
	rep(5, ok && distinct && b1 == 10 && e.Refresh("V1") == nil, "4 distinct errors; rejects leave no trace; usable")
	ok = true // 6: propagation visits only the chain, independent of m views
	for _, m := range []int{100, 1000, 10000} {
		ns := []dep.Node{{Name: "B0", Kind: dep.Base}, {Name: "C1", Kind: dep.View, Deps: []string{"B0"}}, {Name: "C2", Kind: dep.View, Deps: []string{"C1"}}}
		for i := 0; i < m; i++ {
			u := fmt.Sprintf("U%d", i)
			ns = append(ns, dep.Node{Name: u, Kind: dep.Base}, dep.Node{Name: "W" + u, Kind: dep.View, Deps: []string{u}})
		}
		if reach(dep.New(ns...), "B0") != 2 {
			ok = false
		}
	}
	rep(6, ok, "propagation visits 2 chain nodes for m=100..10000 (edge-bound)")
	e = api.New() // 7: concurrent readers agree; no sleep; race-clean
	_ = e.UpdateBase("B2", 40)
	for _, n := range vn {
		_ = e.Refresh(n)
	}
	want := [3]int{50, 70, 120}
	rc := make(chan bool, 64)
	for i := 0; i < 64; i++ {
		go func() {
			same := true
			for j, n := range vn {
				v, _ := e.Value(n)
				if v != want[j] || e.IsStale(n) {
					same = false
				}
			}
			rc <- same
		}()
	}
	ok = true
	for i := 0; i < 64; i++ {
		if !<-rc {
			ok = false
		}
	}
	rep(7, ok, "64 concurrent readers see identical V1=50 V2=70 V3=120, valid")
	if fail {
		os.Exit(1)
	}
}
