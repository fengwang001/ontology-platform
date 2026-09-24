package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sync"
	"sync/atomic"

	"ontology/agg"
	"ontology/api"
	"ontology/delta"
)

func f(c, s, lo, hi int64) api.Four {
	return api.Four{Count: c, Sum: s, Min: lo, Max: hi, HasMin: true, HasMax: true}
}
func reportAll(names []string, oks []bool) {
	for i := range names {
		if oks[i] {
			fmt.Println("OK  " + names[i])
		} else {
			fmt.Println("FAIL " + names[i])
			panic("demo checks failed")
		}
	}
}
func reader(start chan struct{}, v *api.View, ref api.Four, diff *atomic.Bool, wg *sync.WaitGroup) {
	defer wg.Done()
	<-start
	for range 200 {
		if v.Snapshot("g") != ref {
			diff.Store(true)
		}
	}
}
func main() {
	v, six := api.New(8), true
	want := []api.Four{f(1, 5, 5, 5), f(2, 7, 2, 5), f(3, 16, 2, 9), f(2, 7, 2, 5), f(3, 9, 2, 5), f(2, 7, 2, 5)}
	for i, e := range []delta.Event{{Key: "g", Val: 5, Op: 1}, {Key: "g", Val: 2, Op: 1}, {Key: "g", Val: 9, Op: 1}, {Key: "g", Val: 9, Op: 2}, {Key: "g", Val: 2, Op: 1}, {Key: "g", Val: 2, Op: 2}} {
		_ = v.Feed([]delta.Event{e})
		six = six && v.Snapshot("g") == want[i]
	}
	h := api.New(1)
	before := h.Snapshot("k")
	_ = h.Feed([]delta.Event{{Key: "k", Val: 42, Op: 1}, {Key: "k", Val: 42, Op: 2}})
	e := api.New(1)
	f0 := e.Snapshot("x")
	_ = e.Feed([]delta.Event{{Key: "x", Val: 0, Op: 1}})
	fx := e.Snapshot("x")
	d := api.New(1)
	_ = d.Feed([]delta.Event{{Key: "g", Val: 2, Op: 1}, {Key: "g", Val: 2, Op: 1}, {Key: "g", Val: 2, Op: 2}})
	rng, pool, rv, match := rand.New(rand.NewPCG(1, 2)), []int64{}, api.New(1), true
	ref := func() (t api.Four) {
		t.HasMin, t.HasMax, t.Min, t.Max = len(pool) > 0, len(pool) > 0, 1<<62, -1<<62
		for _, x := range pool {
			t.Count++
			t.Sum += x
			t.Min, t.Max = min(t.Min, x), max(t.Max, x)
		}
		if !t.HasMin {
			t.Min, t.Max = 0, 0
		}
		return
	}
	for range 5000 {
		ev := delta.Event{Key: "g", Val: int64(rng.IntN(199) - 99), Op: 1}
		if len(pool) > 0 && rng.IntN(3) == 0 {
			i := rng.IntN(len(pool))
			val := pool[i]
			ev.Val, ev.Op, pool = val, 2, append(pool[:i], pool[i+1:]...)
		} else {
			pool = append(pool, ev.Val)
		}
		if rv.Feed([]delta.Event{ev}) != nil || rv.Snapshot("g") != ref() {
			match = false
			break
		}
	}
	w := api.New(1)
	_ = w.Feed([]delta.Event{{Key: "g", Val: math.MaxInt64, Op: 1}})
	distinct := errors.Is(w.Feed([]delta.Event{{Key: "g", Val: 1, Op: 1}}), agg.ErrSumOverflow)
	distinct = distinct && errors.Is(w.Feed([]delta.Event{{Key: "g", Val: 9, Op: 2}}), agg.ErrRetractMissing)
	distinct = distinct && errors.Is(w.Feed([]delta.Event{{Key: "a", Val: 1, Op: 1}}), api.ErrTooManyGroups)
	cv := api.New(1)
	_ = cv.Feed([]delta.Event{{Key: "g", Val: 7, Op: 1}, {Key: "g", Val: -3, Op: 1}})
	r, start := cv.Snapshot("g"), make(chan struct{})
	var wg sync.WaitGroup
	var diff atomic.Bool
	for range 16 {
		wg.Add(1)
		go reader(start, cv, r, &diff, &wg)
	}
	close(start)
	wg.Wait()
	names := []string{"six-step tuples", "retract is inverse of insert", "empty MIN/MAX absent, distinct from zero", "dup value: one retraction keeps the other copy", "random stream matches full recompute", "three distinct sentinel errors", "state unchanged after rejection", "visited-count sublinear in m", "concurrent readers get identical tuples"}
	oks := []bool{six, h.Snapshot("k") == before, !f0.HasMin && !f0.HasMax && fx.Min == 0 && fx.HasMin, d.Snapshot("g") == f(1, 2, 2, 2), match, distinct, w.Snapshot("g") == f(1, math.MaxInt64, math.MaxInt64, math.MaxInt64), agg.AccessBoundOK(), !diff.Load()}
	reportAll(names, oks)
}
