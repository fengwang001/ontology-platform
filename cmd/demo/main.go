package main

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	s := "OK"
	if !ok {
		s, failed = "FAIL", true
	}
	fmt.Println(s, name)
}

func main() {
	g := api.New()
	g.AddTask("T1", 5)
	g.AddTask("T2", 3)
	g.AddTask("T3", 1)
	g.AddResource("Rx")
	g.AddResource("Ry")
	for _, u := range [][2]string{{"T1", "Rx"}, {"T3", "Rx"}, {"T2", "Ry"}, {"T3", "Ry"}} {
		_ = g.Use(u[0], u[1])
	}
	g1, _ := g.Acquire("T3", "Rx")
	eff3, _ := g.EffectivePriority("T3")
	ceilX := g.SystemCeiling()
	g2, _ := g.Acquire("T2", "Ry")
	g3, _ := g.Acquire("T1", "Rx")
	e4 := g.Release("T3", "Rx")
	g5, _ := g.Acquire("T1", "Rx")
	g6, _ := g.Acquire("T2", "Ry")
	e7 := g.Release("T1", "Rx")
	g8, _ := g.Acquire("T2", "Ry")
	check("eight-step granted/blocked sequence", g1 && !g2 && !g3 && e4 == nil && g5 && !g6 && e7 == nil && g8)
	check("ceilings Rx=5 Ry=3, T3 boosted to 5", ceilX == 5 && eff3 == 5 && g.SystemCeiling() == 3)
	m, holder := api.New(), map[string]int{}
	for i := 0; i < 5; i++ {
		m.AddTask(fmt.Sprintf("t%d", i), i+1)
	}
	for j := 0; j < 4; j++ {
		m.AddResource(fmt.Sprintf("r%d", j))
		for i := 0; i < 5; i++ {
			_ = m.Use(fmt.Sprintf("t%d", i), fmt.Sprintf("r%d", j))
		}
	}
	consistent, seed := true, uint32(42)
	for k := 0; k < 3000 && consistent; k++ {
		seed = seed*1664525 + 1013904223
		ti, r := int(seed%5), fmt.Sprintf("r%d", (seed>>8)%4)
		sys := 0
		for _, h := range holder {
			if h != ti {
				sys = 5
			}
		}
		switch h, held := holder[r]; {
		case held && h == ti:
			consistent = m.Release(fmt.Sprintf("t%d", ti), r) == nil
			delete(holder, r)
		case held:
			got, err := m.Acquire(fmt.Sprintf("t%d", ti), r)
			consistent = err == nil && !got
		default:
			got, _ := m.Acquire(fmt.Sprintf("t%d", ti), r)
			want := ti+1 > sys
			consistent = got == want
			if want {
				holder[r] = ti
			}
		}
		ceil := 5 * min(len(holder), 1)
		consistent = consistent && m.SystemCeiling() == ceil
	}
	check("naive reference consistency", consistent)
	f := api.New()
	f.AddTask("a", 5)
	f.AddTask("b", 2)
	f.AddResource("R")
	_ = f.Use("a", "R")
	_, _ = f.Acquire("a", "R") // a holds R, system ceiling 5
	fault := func(fn func() error, want error) bool { return fn() == want }
	ok := fault(func() error { _, e := f.Acquire("ghost", "R"); return e }, api.ErrUnknownTask) &&
		fault(func() error { _, e := f.Acquire("a", "nope"); return e }, api.ErrUnknownResource) &&
		fault(func() error { _, e := f.Acquire("a", "R"); return e }, api.ErrAlreadyHeld) &&
		fault(func() error { return f.Release("b", "R") }, api.ErrNotHeld)
	check("four distinguishable errors, state unchanged", ok && f.SystemCeiling() == 5 && f.Release("a", "R") == nil)
	lm := api.New()
	lm.AddTask("H", 100000)
	lm.AddTask("L", 1)
	lm.AddResource("rx")
	_ = lm.Use("L", "rx")
	ok = true
	for _, n := range []int{100, 1000, 10000} {
		for i := 0; i < n; i++ {
			r := fmt.Sprintf("m%d", i)
			lm.AddResource(r)
			_ = lm.Use("H", r)
			got, _ := lm.Acquire("H", r)
			ok = ok && got
		}
		block, _ := lm.Acquire("L", "rx")
		ok = ok && !block && lm.SystemCeiling() == 100000
		for i := 0; i < n; i++ {
			ok = ok && lm.Release("H", fmt.Sprintf("m%d", i)) == nil
		}
		got, _ := lm.Acquire("L", "rx")
		ok = ok && got && lm.Release("L", "rx") == nil
	}
	check("large-m grant/block correct; check-count O(1) proven by lock.TestAcquireCheckCount", ok)
	c := api.New()
	const np = 4
	held, bad := [np]int32{}, int32(0)
	for i := 0; i < 2*np; i++ {
		t, r := fmt.Sprintf("c%d", i), fmt.Sprintf("cr%d", i%np)
		c.AddTask(t, 10+i)
		c.AddResource(r)
		_ = c.Use(t, r)
	}
	var wg sync.WaitGroup
	for i := 0; i < 2*np; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			t, r := fmt.Sprintf("c%d", i), fmt.Sprintf("cr%d", i%np)
			for k := 0; k < 3000; k++ {
				if got, _ := c.Acquire(t, r); got {
					if atomic.AddInt32(&held[i%np], 1) != 1 {
						atomic.AddInt32(&bad, 1)
					}
					atomic.AddInt32(&held[i%np], -1)
					_ = c.Release(t, r) // cannot fail: this goroutine holds r
				}
			}
		}(i)
	}
	wg.Wait()
	check("concurrent mutual exclusion", bad == 0 && c.SystemCeiling() == 0)
	check("SelfCheck", api.New().SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
