// Command demo prints OK/FAIL judgements for the schema-evolution task.
// It reads no arguments and performs no network access.
package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/evol"
	"ontology/sch"
)

var failed bool

func check(tag string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status, failed = "FAIL", true
	}
	fmt.Printf("%s %s %s\n", tag, status, detail)
}

func show(m map[string]int) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	s := "{"
	for i, k := range ks {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("%s:%d", k, m[k])
	}
	return s + "}"
}

func main() {
	a := api.New()
	check("selfcheck", a.SelfCheck() == nil, "four built-in invariants")

	r1, _ := a.Write(1, map[string]int{"a": 9, "b": 7})
	r2, _ := a.Write(2, map[string]int{"a": 5})
	r3, _ := a.Write(3, map[string]int{"a": 1, "c": 2, "d": 3})
	m2, _ := a.Read(r1, 3)
	m4, _ := a.Read(r2, 3)
	m6, _ := a.Read(r3, 2)
	m7, _ := a.Read(r2, 2)
	m8, _ := a.Read(r1, 1)
	check("step2", show(m2) == "{a:9 c:10 d:20}", show(m2)+" (b correctly absent)")
	check("step4", m4["c"] == 10, show(m4)+" (c=10 writer-frozen, wrong impl gives 30)")
	check("step6", m6["b"] == 2, show(m6)+" (b=2 pre-removal, wrong impl gives 0)")
	check("step7", show(m7) == "{a:5 b:2 c:10}", show(m7))
	check("step8", show(m8) == "{a:9 b:7}", show(m8))

	okBC := true // backward compat: fully written v1..v3 keep common fields
	for W, vals := range []map[string]int{{"a": 1, "b": 2}, {"a": 1, "b": 2, "c": 10}, {"a": 1, "c": 30, "d": 20}} {
		rec, _ := a.Write(W+1, vals)
		for R := W + 1; R <= 3; R++ {
			got, _ := a.Read(rec, R)
			for _, f := range []string{"a", "b", "c", "d"} {
				if v, inW := vals[f]; inW {
					if g, inR := got[f]; inR && g != v {
						okBC = false
					}
				}
			}
		}
	}
	f2, _ := a.Read(r1, 2)
	f3, _ := a.Read(r1, 3)
	check("compat+freeze", okBC && f2["c"] == 10 && f3["c"] == 10, "common fields preserved; c stays 10 across R")

	sentinels := []error{evol.ErrBadWriteVersion, evol.ErrFieldOutOfScope, evol.ErrBadReadVersion}
	distinct := sentinels[0] != sentinels[1] && sentinels[1] != sentinels[2]
	_, e1 := a.Write(0, nil)
	_, e2 := a.Write(1, map[string]int{"z": 1})
	_, e3 := a.Read(r1, 9)
	after, _ := a.Read(r1, 3)
	noTrace := errors.Is(e1, evol.ErrBadWriteVersion) && errors.Is(e2, evol.ErrFieldOutOfScope) &&
		errors.Is(e3, evol.ErrBadReadVersion) && show(after) == "{a:9 c:10 d:20}"
	check("errors+notrace", distinct && noTrace, "three distinct sentinels; usable after rejection")

	okBig := true
	for _, m := range []int{100, 1000, 10000} { // x in all versions; late appears only at m
		hist := make([]sch.Version, m)
		for v := range hist {
			hist[v].Fields = []sch.Field{{Name: "x", Def: 1}}
		}
		hist[m-1].Fields = append(hist[m-1].Fields, sch.Field{Name: "late", Def: 7})
		eng := evol.NewEngine(sch.New(hist))
		rec, err := eng.Write(1, map[string]int{"x": 1})
		got, err2 := eng.Read(rec, m)
		if err != nil || err2 != nil || got["late"] != 7 {
			okBig = false
		}
	}
	check("large-m", okBig, "late-field default correct at m=100/1000/10000 (O(1) pinned by sch tests)")

	var wg sync.WaitGroup
	base, _ := a.Read(r2, 3)
	var concOK atomic.Bool
	concOK.Store(true)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(R int) {
			defer wg.Done()
			want, _ := a.Read(r2, R)
			for i := 0; i < 200; i++ {
				got, err := a.Read(r2, R)
				if err != nil || show(got) != show(want) {
					concOK.Store(false)
				}
				if _, e := a.Write(1, map[string]int{"a": i}); e != nil { // interleaved new records
					concOK.Store(false)
				}
			}
		}(g%3 + 1)
	}
	wg.Wait()
	check("concurrent", concOK.Load() && show(base) == "{a:5 c:10 d:20}", "identical reads per R under interleaved writes")

	if failed {
		os.Exit(1)
	}
}
