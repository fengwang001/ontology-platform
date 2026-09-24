// Command demo exercises the per-key debounce refresher and prints OK/FAIL.
package main

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/thr"
)

func main() {
	bad := 0
	check := func(name string, cond bool) {
		if cond {
			fmt.Println("OK " + name)
		} else {
			fmt.Println("FAIL " + name)
			bad++
		}
	}

	// --- section 3 eight steps: per-step refresh output, steps 5 and 6 ---
	r, _ := api.New(3, 8)
	rec := func(k string, t int64, v string) {
		if e := r.Record(k, t, v); e != nil {
			bad++
		}
	}
	rec("k", 0, "a")
	rec("k", 2, "b")
	rec("m", 2, "x")
	s4 := r.Tick(4) // Due=5 for both: nothing
	s5ok := r.Record("k", 5, "c") == nil
	s6 := r.Tick(5) // equality boundary: only m fires
	rec("m", 5, "y")
	s8 := r.Stop(8)
	check("eight-steps per-step output; step5 merges, step6 fires m only",
		len(s4) == 0 && s5ok &&
			reflect.DeepEqual(s6, []api.Refresh{{Key: "m", N: 1, Val: "x"}}) &&
			reflect.DeepEqual(s8, []api.Refresh{{Key: "k", N: 3, Val: "c"}, {Key: "m", N: 1, Val: "y"}}))

	// --- equality boundaries: Tick with ==Due fires; gap exactly W merges ---
	q, _ := api.New(3, 4)
	_ = q.Record("q", 0, "a")
	_ = q.Record("q", 3, "b") // no Tick between: one open batch even at gap==W
	b := len(q.Tick(2)) == 0 &&
		reflect.DeepEqual(q.Tick(6), []api.Refresh{{Key: "q", N: 2, Val: "b"}})
	check("equality boundaries: Tick==Due fires, same-key gap==W merges", b)

	// --- Stop view equals the naive reference; fired <= accepted ---
	ref := map[string]string{"k": "c", "m": "y"}
	check("Stop view == naive last-accepted reference", reflect.DeepEqual(r.View(), ref))
	check("fired batches (3) <= accepted changes (5)", r.Fired() <= 5 && r.Fired() == 3)

	// --- four pairwise-distinct decidable sentinel errors ---
	_, e1 := api.New(0, 1)
	_, e2 := api.New(1, 0)
	e3 := r.Record("", 9, "z")
	e4 := r.Record("k", 1, "back")
	d, _ := api.New(3, 1)
	_ = d.Record("a", 0, "1")
	e5 := d.Record("b", 0, "2")
	check("four distinct decidable errors",
		errors.Is(e1, thr.ErrBadParam) && errors.Is(e2, thr.ErrBadParam) &&
			errors.Is(e3, thr.ErrEmptyKey) && errors.Is(e4, thr.ErrClockBack) &&
			errors.Is(e5, thr.ErrTooMany) &&
			thr.ErrBadParam != thr.ErrEmptyKey && thr.ErrEmptyKey != thr.ErrClockBack &&
			thr.ErrClockBack != thr.ErrTooMany)

	// --- rejected calls leave no trace and the instance stays usable ---
	v0, f0 := d.View(), d.Fired()
	_ = d.Record("", 1, "x")
	_ = d.Record("a", -1, "x")
	_ = d.Record("b", 0, "2")
	trace := reflect.DeepEqual(d.View(), v0) && d.Fired() == f0
	usable := reflect.DeepEqual(d.Stop(2), []api.Refresh{{Key: "a", N: 1, Val: "1"}})
	check("rejection leaves no trace; instance still usable", trace && usable)

	// --- large m: ordered lookup observable without exposing the probe ---
	largeOK := true
	for _, m := range []int{100, 1000, 10000} {
		h, _ := api.New(1000, int64(m)+1)
		for i := 0; i < m; i++ {
			if e := h.Record(fmt.Sprintf("k%05d", i), 0, "v"); e != nil {
				largeOK = false
			}
		}
		if got := h.Tick(0); len(got) != 0 { // all Due=1000: zero fires at every m
			largeOK = false
		}
		_ = h.Record("~due~", 998, "d")
		for i := 0; i < m; i++ { // postpone the m batches to Due=1999
			_ = h.Record(fmt.Sprintf("k%05d", i), 999, "w")
		}
		got := h.Tick(1998) // exactly one root probe reaches the due batch
		if len(got) != 1 || got[0].Key != "~due~" {
			largeOK = false
		}
	}
	check("large-m ordered Tick independent of m (probe count pinned in thr test)", largeOK)

	// --- concurrent read-only access: field-identical snapshots ---
	c, _ := api.New(3, 64)
	for i := 0; i < 16; i++ {
		_ = c.Record(string(rune('a'+i)), int64(i), "v")
	}
	c.Stop(100)
	const N = 16
	vs, fs := make([]map[string]string, N), make([]int, N)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for j := 0; j < 100; j++ {
				vs[g], fs[g] = c.View(), c.Fired()
				_ = c.SelfCheck()
			}
		}(g)
	}
	close(start)
	wg.Wait()
	same := true
	for g := 1; g < N; g++ {
		if !reflect.DeepEqual(vs[g], vs[0]) || fs[g] != fs[0] {
			same = false
		}
	}
	check("concurrent readers get field-identical views and counts", same && r.SelfCheck() == nil)

	if bad > 0 {
		panic("demo checks failed")
	}
}
