// Command demo 验证延迟物化引擎的关键判定，全部通过退出码 0，输出不超过 10 行。
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/dep"
)

var (
	fails int
	fired []string
)

func check(ok bool, line string) {
	tag := ": OK"
	if !ok {
		tag, fails = ": FAIL", fails+1
	}
	fmt.Println(line + tag)
}

func prof(k string, f func([]int) int) func([]int) int {
	return func(v []int) int { fired = append(fired, k); return f(v) }
}

func get(t *api.Table, c string) (int, []string) {
	fired = fired[:0]
	v, _ := t.Get(c)
	return v, append([]string(nil), fired...)
}

func main() {
	eq := func(g []string, w ...string) bool { return strings.Join(g, ",") == strings.Join(w, ",") }
	sum := func(v []int) int { return v[0] + v[1] }
	dbl := func(v []int) int { return v[0] * 2 }
	spec := dep.Spec{Columns: []dep.Column{
		{Name: "a", Base: true, Initial: 2}, {Name: "b", Base: true, Initial: 3},
		{Name: "c", Base: true, Initial: 4},
		{Name: "d", Deps: []string{"a", "b"}, Fn: prof("d", sum)},
		{Name: "e", Deps: []string{"d"}, Fn: prof("e", dbl)},
		{Name: "f", Deps: []string{"c", "e"}, Fn: prof("f", sum)},
	}}
	tb, err := api.New(spec)
	if err != nil {
		fmt.Println("New: FAIL", err)
		os.Exit(1)
	}
	v1, f1 := get(tb, "e") // 1
	v2, f2 := get(tb, "d") // 2
	v3, f3 := get(tb, "f") // 3
	check(eq(f1, "d", "e") && v1 == 10 && len(f2) == 0 && v2 == 5 && eq(f3, "f") && v3 == 14,
		"steps1-3: fire d,e; reuse d; fire f(e reused); 10,5,14")
	tb.Set("a", 5) // 4
	v5, f5 := get(tb, "d")
	v6, f6 := get(tb, "f")
	v7, f7 := get(tb, "e")
	check(v5 == 8 && eq(f5, "d") && v6 == 20 && eq(f6, "e", "f") && v7 == 16 && len(f7) == 0,
		"step4 Set(a,5); steps5-7: d=8 fire d, f=20 fire e,f(d reused), e=16 cached")
	// 朴素全量即时重算的结果在此基列取值下就是 d=8,e=16,f=20。
	vd, _ := tb.Get("d")
	ve2, _ := tb.Get("e")
	vf, _ := tb.Get("f")
	check(vd == 8 && ve2 == 16 && vf == 20, "agree with naive full recomputation (8,16,20)")
	fired = fired[:0]
	tb.Get("d")
	tb.Get("e")
	tb.Get("f")
	check(len(fired) == 0, "cache consistency: cached reads invoke 0 Fn")
	tb2, _ := api.New(spec)
	tb2.Get("f")
	tb2.Set("c", 40)
	ve3, fe := get(tb2, "e")
	vfx, ff := get(tb2, "f")
	check(ve3 == 10 && len(fe) == 0 && vfx == 50 && eq(ff, "f"),
		"invalidation: e cache kept, only f recomputed to 50 after Set(c,40)")
	id := func(v []int) int { return v[0] }
	mk := func(n string, d ...string) dep.Column { return dep.Column{Name: n, Deps: d, Fn: id} }
	b := dep.Column{Name: "a", Base: true}
	_, eCyc := api.New(dep.Spec{Columns: []dep.Column{b, mk("x", "y"), mk("y", "x")}})
	_, eUnd := api.New(dep.Spec{Columns: []dep.Column{b, mk("x", "zzz")}})
	_, eUnk := tb.Get("nope")
	eSet := tb.Set("d", 9)
	distinct := errors.Is(eCyc, dep.ErrCyclic) && errors.Is(eUnd, dep.ErrUndeclaredDep) &&
		errors.Is(eUnk, api.ErrUnknown) && errors.Is(eSet, api.ErrSetDerived)
	va, fa := get(tb, "f")
	check(distinct && va == 20 && len(fa) == 0,
		"four distinct sentinel errors; state unchanged after rejected ops")
	chainOK := true
	for _, m := range []int{100, 1000, 10000} {
		n := 0
		inc := func(v []int) int { n++; return v[0] + 1 }
		tc, _ := api.New(chainSpec(m, inc))
		tc.Get(fmt.Sprintf("c%d", m))
		tc.Set("a", 0)
		n = 0
		if v, _ := tc.Get("c1"); v != 1 || n != 1 {
			chainOK = false
		}
	}
	check(chainOK, "chain m=100,1000,10000: after invalidate Get(c1) computes exactly 1")
	tb3, _ := api.New(spec)
	cols := []string{"a", "b", "c", "d", "e", "f"}
	ref := map[string]int{}
	for _, c := range cols {
		ref[c], _ = tb3.Get(c)
	}
	concOK := true
	var wg sync.WaitGroup
	bad := make(chan bool, 16)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < 100; r++ {
				for _, c := range cols {
					if v, _ := tb3.Get(c); v != ref[c] {
						bad <- true
						return
					}
				}
			}
			bad <- false
		}()
	}
	wg.Wait()
	close(bad)
	for x := range bad {
		concOK = concOK && !x
	}
	check(concOK && tb.SelfCheck() == nil, "concurrent reads agree column-by-column; SelfCheck passes")
	if fails > 0 {
		os.Exit(1)
	}
}
func chainSpec(m int, fn func([]int) int) dep.Spec {
	cs := []dep.Column{{Name: "a", Base: true, Initial: 0}}
	pre := "a"
	for i := 1; i <= m; i++ {
		n := fmt.Sprintf("c%d", i)
		cs = append(cs, dep.Column{Name: n, Deps: []string{pre}, Fn: fn})
		pre = n
	}
	return dep.Spec{Columns: cs}
}
