package main

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/dep"
	"ontology/mat"
)

var failed bool

func check(name string, ok bool) {
	s := "OK"
	if !ok {
		s, failed = "FAIL", true
	}
	fmt.Println(s, name)
}

func tracedSpec(rec *[]string) dep.Spec {
	return dep.Spec{
		Base: map[string]int{"a": 2, "b": 3, "c": 4},
		Derived: map[string]dep.Derived{
			"d": {Deps: []string{"a", "b"}, Fn: func(v []int) int { *rec = append(*rec, "d"); return v[0] + v[1] }},
			"e": {Deps: []string{"d"}, Fn: func(v []int) int { *rec = append(*rec, "e"); return v[0] * 2 }},
			"f": {Deps: []string{"c", "e"}, Fn: func(v []int) int { *rec = append(*rec, "f"); return v[0] + v[1] }},
		},
	}
}
func warm() (*api.Engine, map[string]int) {
	eng, _ := api.New(tracedSpec(&[]string{}))
	want := map[string]int{"a": 2, "b": 3, "c": 4, "d": 5, "e": 10, "f": 14}
	for c := range want {
		eng.Get(c)
	}
	return eng, want
}

// sevenSteps 逐步核验第三节七行表：触发计算列、返回值、缓存复用。arg<0=Get，否则 Set(col,arg)。
func sevenSteps() bool {
	var rec []string
	eng, _ := api.New(tracedSpec(&rec))
	cols := []string{"e", "d", "f", "a", "d", "f", "e"}
	fired := []string{"d,e", "", "f", "", "d", "e,f", ""}
	args := []int{-1, -1, -1, 5, -1, -1, -1}
	wants := []int{10, 5, 14, 0, 8, 20, 16}
	for i, c := range cols {
		rec = nil
		if args[i] < 0 {
			if v, err := eng.Get(c); err != nil || v != wants[i] {
				return false
			}
		} else if eng.Set(c, args[i]) != nil {
			return false
		}
		if strings.Join(rec, ",") != fired[i] {
			return false
		}
	}
	return true
}
func errorsAndAtomic() bool {
	eng, want := warm()
	_, gerr := eng.Get("zzz")
	serr := eng.Set("zzz", 1)
	derr := eng.Set("d", 1)
	fn0 := func(v []int) int { return v[0] }
	cyc := dep.Spec{Derived: map[string]dep.Derived{"x": {Deps: []string{"y"}, Fn: fn0}, "y": {Deps: []string{"x"}, Fn: fn0}}}
	bad := dep.Spec{Base: map[string]int{"a": 1}, Derived: map[string]dep.Derived{"x": {Deps: []string{"y"}, Fn: fn0}}}
	_, cerr := api.New(cyc)
	_, uerr := api.New(bad)
	if gerr != mat.ErrUnknownColumn || serr != mat.ErrUnknownColumn || derr != mat.ErrSetDerived ||
		cerr != dep.ErrCycle || uerr != dep.ErrUnknownDep {
		return false
	}
	for c, w := range want {
		if v, _ := eng.Get(c); v != w {
			return false
		}
	}
	return true
}

// chainConst1 大 m 链：整条算全后 Get(c1) 零重算；Set(a) 失效后 Get(c1) 只重算 1 次。
func chainConst1() bool {
	for _, m := range []int{100, 1000, 10000} {
		cnt := 0
		s := dep.Spec{Base: map[string]int{"a": 1}, Derived: map[string]dep.Derived{}}
		prev := "a"
		for i := 1; i <= m; i++ {
			n := fmt.Sprintf("c%d", i)
			s.Derived[n] = dep.Derived{Deps: []string{prev}, Fn: func(v []int) int { cnt++; return v[0] + 1 }}
			prev = n
		}
		eng, _ := api.New(s)
		if v, _ := eng.Get(fmt.Sprintf("c%d", m)); v != m+1 || cnt != m {
			return false
		}
		cnt = 0
		if _, _ = eng.Get("c1"); cnt != 0 {
			return false
		}
		eng.Set("a", 7)
		if v, _ := eng.Get("c1"); v != 8 || cnt != 1 {
			return false
		}
	}
	return true
}

// concurrent 缓存就绪后并发读同一实例，逐列一致。
func concurrent() bool {
	eng, want := warm()
	var bad atomic.Int32
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < 50; r++ {
				for c, w := range want {
					if v, err := eng.Get(c); err != nil || v != w {
						bad.Add(1)
					}
				}
			}
		}()
	}
	wg.Wait()
	return bad.Load() == 0
}
func main() {
	eng, err := api.New(tracedSpec(&[]string{}))
	check("new+selfcheck(4 invariants)", err == nil && eng.SelfCheck() == nil)
	check("seven-step trace", sevenSteps())
	check("four decidable errors, state intact", errorsAndAtomic())
	check("chain m=100..10000 recompute==1", chainConst1())
	check("concurrent reads consistent", concurrent())
	if failed {
		fmt.Println("RESULT FAIL")
	} else {
		fmt.Println("RESULT OK")
	}
}
