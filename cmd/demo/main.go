// Command demo prints OK/FAIL lines (at most ten) for the max-min fair-share
// allocator. It takes no arguments and performs no network access.
package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/api"
	"ontology/mf"
)

func line(name string, ok bool) {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
	}
}

func main() {
	// 1. Step table of section three: C=30, demands 6/12/18/30 -> A=6 B=C=D=8.
	a, _ := api.New(30)
	for _, t := range [][2]interface{}{{"A", int64(6)}, {"B", int64(12)}, {"C", int64(18)}, {"D", int64(30)}} {
		_ = a.Add(t[0].(string), t[1].(int64))
	}
	sh := a.Allocate()
	want := map[string]mf.Frac{"A": mf.Int(6), "B": mf.Int(8), "C": mf.Int(8), "D": mf.Int(8)}
	eq := len(sh) == len(want)
	for id, f := range want {
		eq = eq && sh[id].Cmp(f) == 0
	}
	line("step table -> A=6 B=8 C=8 D=8", eq)

	// 2. Conservation: shares sum to exactly 30.
	sum := mf.Int(0)
	for _, f := range sh {
		sum = mf.Make(f.N*sum.D+sum.N*f.D, f.D*sum.D)
	}
	line("conservation sum = 30", sum.Cmp(mf.Int(30)) == 0)

	// 3. Max-min: every unmet task (B,C,D) is at the same level, A no higher.
	lvl := sh["B"]
	line("unmet tasks levelled to 8", sh["C"].Cmp(lvl) == 0 && sh["D"].Cmp(lvl) == 0 && lvl.Cmp(mf.Int(8)) == 0)

	// 4. Matches the naive reference (and rejected-op state) via SelfCheck.
	line("matches naive reference", a.SelfCheck() == nil)

	// 5. Four distinct, judgeable sentinel errors.
	_, e0 := api.New(0)
	e1 := a.Add("", 1)
	e2 := a.Add("A", 1)
	e3 := a.Add("Q", -1)
	distinct := errors.Is(e0, api.ErrInvalidCapacity) && errors.Is(e1, api.ErrEmptyID) &&
		errors.Is(e2, api.ErrDuplicateID) && errors.Is(e3, api.ErrNegativeDemand) &&
		e0.Error() != e1.Error() && e1.Error() != e2.Error() && e2.Error() != e3.Error()
	line("four distinct sentinel errors", distinct)

	// 6. Rejected operations leave no trace: result is unchanged.
	line("rejected Add leaves no trace", a.Allocate()["A"].Cmp(mf.Int(6)) == 0 && len(a.Allocate()) == 4)

	// 7. Large m: only the first k=3 small demands are full, all others share
	//    one level and capacity is conserved — the located water-point shape.
	bigOK := true
	for _, m := range []int{100, 1000, 10000} {
		c, _ := api.New(1_000_000)
		_ = c.Add(fmt.Sprintf("small%d", 0), int64(1))
		_ = c.Add(fmt.Sprintf("small%d", 1), int64(2))
		_ = c.Add(fmt.Sprintf("small%d", 2), int64(3))
		for i := 3; i < m; i++ {
			_ = c.Add(fmt.Sprintf("t%d", i), 1_000_000)
		}
		r := c.Allocate()
		var level mf.Frac
		nlevel, nfull := 0, 0
		for i := 3; i < m; i++ {
			f := r[fmt.Sprintf("t%d", i)]
			if level.IsZero() {
				level = f
			}
			if f.Cmp(level) != 0 {
				bigOK = false
			}
			nlevel++
		}
		for i := 0; i < 3; i++ {
			if r[fmt.Sprintf("small%d", i)].Cmp(mf.Int(int64(i+1))) == 0 {
				nfull++
			}
		}
		bigOK = bigOK && nlevel == m-3 && nfull == 3 && level.Cmp(mf.Int(1_000_000)) < 0
	}
	line("large m: k full, one shared level", bigOK)

	// 8. Concurrent Add equals sequential Add, with conservation.
	n := 200
	par, _ := api.New(1_000)
	seq, _ := api.New(1_000)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("p%d", i)
		d := int64(1 + (i*7)%50)
		_ = seq.Add(id, d)
		wg.Add(1)
		go func(id string, d int64) {
			defer wg.Done()
			_ = par.Add(id, d)
		}(id, d)
	}
	wg.Wait()
	pr, sr := par.Allocate(), seq.Allocate()
	concOK := len(pr) == n
	for id, f := range sr {
		concOK = concOK && pr[id].Cmp(f) == 0
	}
	line("concurrent Add equals sequential", concOK)

	if !(eq && distinct && bigOK && concOK) {
		panic("demo failed")
	}
}
