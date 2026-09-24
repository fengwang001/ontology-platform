// Command demo verifies the materialized-view DAG behavior end to end.
package main

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"ontology/api"
)

func report(tag string, cond bool) {
	if cond {
		fmt.Println("OK  " + tag)
	} else {
		fmt.Println("FAIL " + tag)
	}
}

func main() {
	sum := func(x ...int64) int64 { return x[0] + x[1] }
	dbl := func(x ...int64) int64 { return x[0] * 2 }
	dec := func(x ...int64) int64 { return x[0] - 1 }

	// §3 sequence: E,F registered before C,D (forward references).
	r := api.New()
	for _, v := range []struct {
		n string
		d []string
		f func(...int64) int64
	}{{"A", nil, nil}, {"B", nil, nil}, {"E", []string{"C", "D"}, sum},
		{"F", []string{"D"}, dec}, {"C", []string{"A", "B"}, sum}, {"D", []string{"A"}, dbl}} {
		if err := r.AddView(v.n, v.d, v.f); err != nil {
			panic(err)
		}
	}
	for _, ab := range [][2]int64{{1, 2}, {1, 20}, {10, 20}} {
		if err := r.Set("A", ab[0]); err != nil || r.Set("B", ab[1]) != nil {
			panic("set")
		}
		if err := r.Recompute(); err != nil {
			panic(err)
		}
	}
	got := [6]int64{}
	for i, n := range []string{"A", "B", "C", "D", "E", "F"} {
		got[i], _, _ = r.Get(n)
	}
	report(fmt.Sprintf("three-phase values=%v", got),
		got == [6]int64{10, 20, 30, 20, 50, 19})

	if err := r.AddView("M", []string{"N"}, nil); err != nil {
		panic(err)
	}
	report("cycle M<->N rejected", errors.Is(r.AddView("N", []string{"M"}, nil), api.ErrCycle))

	// Dedup: a view invalidated twice (diamond) is evaluated once.
	d := api.New()
	calls := map[string]int{}
	cf := func(n string, g func(...int64) int64) func(...int64) int64 {
		return func(x ...int64) int64 {
			calls[n]++
			return g(x...)
		}
	}
	_ = d.AddView("a", nil, nil)
	_ = d.AddView("b", []string{"a"}, cf("b", dbl))
	_ = d.AddView("c", []string{"a"}, cf("c", dbl))
	_ = d.AddView("v", []string{"b", "c"}, cf("v", sum))
	_ = d.Set("a", 1)
	_ = d.Recompute()
	report("dedup: each dirty fn once", calls["b"] == 1 && calls["c"] == 1 && calls["v"] == 1)

	// Four distinct, decidable sentinel errors.
	errs := []error{d.AddView("", nil, nil), d.AddView("a", nil, nil),
		r.AddView("N", []string{"M"}, nil), d.Set("zzz", 1)}
	want := []error{api.ErrEmptyName, api.ErrExists, api.ErrCycle, api.ErrUnresolved}
	distinct := true
	for i := range errs {
		if !errors.Is(errs[i], want[i]) {
			distinct = false
		}
	}
	report("four distinct sentinel errors", distinct)

	// Rejected op leaves no trace; registry still usable.
	_, _, gerr := d.Get("zzz")
	vv, _, _ := d.Get("v")
	report("rejection leaves no trace, still usable", errors.Is(gerr, api.ErrUnresolved) && vv == 4)
	_ = distinct

	// Large m: Set only on chain base X; the m independent bases' derived
	// views must never be evaluated (unexported evalCount, observed here
	// solely through fn-call side effects).
	big := api.New()
	wcalls := int64(0)
	const m = 10000
	_ = big.AddView("X", nil, nil)
	_ = big.AddView("Y", []string{"X"}, dbl)
	_ = big.AddView("Z", []string{"Y"}, dbl)
	for i := 0; i < m; i++ {
		wn := fmt.Sprintf("W%d", i)
		_ = big.AddView(wn, nil, nil)
		_ = big.AddView(wn+"P", []string{wn}, func(x ...int64) int64 {
			atomic.AddInt64(&wcalls, 1)
			return x[0]
		})
	}
	_ = big.Set("X", 3)
	_ = big.Recompute()
	z, _, _ := big.Get("Z")
	report("eval count does not grow with m=10000", atomic.LoadInt64(&wcalls) == 0 && z == 12)

	// Concurrent writers/readers: every read belongs to a complete round.
	cr := api.New()
	_ = cr.AddView("x", nil, nil)
	_ = cr.AddView("y", []string{"x"}, dbl)
	const rounds, readers = 500, 8
	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := int64(0); i < rounds; i++ {
			_ = cr.Set("x", i)
			if err := cr.Recompute(); err != nil {
				panic(err)
			}
		}
		stop.Store(true)
	}()
	var legal atomic.Bool
	legal.Store(true)
	for k := 0; k < readers; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				if y, ok, err := cr.Get("y"); err != nil || (ok && (y%2 != 0 || y < 0 || y > 2*(rounds-1))) {
					legal.Store(false)
				}
			}
		}()
	}
	wg.Wait()
	yf, _, _ := cr.Get("y")
	report("concurrent reads always committed-round values", legal.Load() && yf == 2*(rounds-1))
	report("SelfCheck", api.New().SelfCheck() == nil)
}
