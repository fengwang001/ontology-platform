// Demo: exercises every invariant of the Count-Min Sketch and prints
// one OK/FAIL line per check. Exit code 0 only if all checks pass.
package main

import (
	"fmt"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/ch"
)

var fails int

func report(ok bool, line string) {
	if !ok {
		fails++
		fmt.Println("FAIL " + line)
		return
	}
	fmt.Println("OK " + line)
}

func add(a *api.API, k, c int64) { _ = a.Add(k, c) }

func main() {
	// 1. Section 3: hit columns via ch.
	cols := true
	for k, want := range map[int64][3]int{2: {2, 5, 1}, 5: {5, 5, 4}, 11: {5, 5, 4}} {
		for j := 1; j <= 3; j++ {
			cols = cols && ch.Col(j, 6, k) == want[j-1]
		}
	}
	report(cols, "sec3 hit columns (2,5,1) (5,5,4) (5,5,4)")
	// 2. Section 3: adds, derived per-row values, and the three queries.
	a, _ := api.New(6, 3)
	add(a, 2, 4)
	add(a, 5, 2)
	add(a, 11, 1)
	q2, _ := a.Query(2)
	q5, _ := a.Query(5)
	q11, _ := a.Query(11)
	report(q2 == 4 && q5 == 3 && q11 == 3,
		"sec3 rows r1{c2:4,c5:3} r2{c5:7} r3{c1:4,c4:3} Query(2)=4 Query(5)=3 Query(11)=3")
	// 3. Never underestimate vs exact map, random keys, several sizes.
	rng, under := rand.New(rand.NewSource(1)), true
	for _, m := range []int{100, 1000, 10000} {
		s, _ := api.New(8, 3)
		ref := map[int64]int64{}
		for i := 0; i < m; i++ {
			k, c := rng.Int63n(50), 1+rng.Int63n(9)
			add(s, k, c)
			ref[k] += c
		}
		for k, c := range ref {
			if q, _ := s.Query(k); q < c {
				under = false
			}
		}
	}
	report(under, "never underestimate (m=100,1000,10000)")
	// 4. Single key exact.
	s1, _ := api.New(6, 3)
	add(s1, 7, 9)
	g1, _ := s1.Query(7)
	report(g1 == 9, "single key exact")
	// 5. Exact-map reference: collision-free equality and colliding >=.
	cf, _ := api.New(9, 3)
	exact := true
	for k := int64(0); k < 9; k++ {
		add(cf, k, k+1)
	}
	for k := int64(0); k < 9; k++ {
		q, _ := cf.Query(k)
		exact = exact && q == k+1
	}
	co, _ := api.New(2, 3)
	refc := map[int64]int64{}
	for _, e := range [][2]int64{{1, 3}, {2, 5}, {3, 7}, {4, 2}} {
		add(co, e[0], e[1])
		refc[e[0]] += e[1]
	}
	for k, c := range refc {
		q, _ := co.Query(k)
		exact = exact && q >= c
	}
	report(exact, "exact-map reference (collision-free ==, colliding >=)")
	// 6. Three distinguishable sentinel errors.
	_, eDim := api.New(0, 3)
	eKey := func() error { s, _ := api.New(6, 3); return s.Add(-1, 1) }()
	eCnt := func() error { s, _ := api.New(6, 3); return s.Add(1, 0) }()
	report(eDim == api.ErrBadDim && eKey == api.ErrBadKey && eCnt == api.ErrBadCount,
		"sentinel errors ErrBadDim/ErrBadKey/ErrBadCount distinct")
	// 7. Rejected ops leave state untouched, sketch still usable.
	st, _ := api.New(6, 3)
	add(st, 2, 4)
	b2, _ := st.Query(2)
	b5, _ := st.Query(5)
	_ = st.Add(-1, 1)
	_ = st.Add(2, 0)
	_, _ = st.Query(-3)
	a2, _ := st.Query(2)
	a5, _ := st.Query(5)
	add(st, 2, 1)
	grew, _ := st.Query(2)
	report(b2 == a2 && b5 == a5 && grew == 5, "rejected ops change nothing, sketch still usable")
	// 8. Query touches exactly d cells regardless of m.
	cost := true
	for _, m := range []int{100, 1000, 10000} {
		s, _ := api.New(64, 4)
		for i := 0; i < m; i++ {
			add(s, int64(i), 1)
		}
		_, _ = s.Query(0)
		cost = cost && s.QueryCostIsDepth()
	}
	report(cost, "query touches exactly d cells (m=100,1000,10000)")
	// 9. Concurrent queries agree key by key.
	cc, _ := api.New(16, 4)
	for k := int64(0); k < 64; k++ {
		add(cc, k, k+1)
	}
	want := make([]int64, 64)
	for k := range want {
		want[k], _ = cc.Query(int64(k))
	}
	var wg sync.WaitGroup
	var bad atomic.Int64
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k, w := range want {
				if q, _ := cc.Query(int64(k)); q != w {
					bad.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	report(bad.Load() == 0, "concurrent queries consistent")
	// 10. Built-in self check.
	report(api.SelfCheck() == nil, "SelfCheck")
	if fails > 0 {
		os.Exit(1)
	}
}
